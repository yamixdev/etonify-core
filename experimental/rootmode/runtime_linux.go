//go:build linux

package rootmode

import (
	"context"
	"io"
	"net"
	"os"
	"path"
	"runtime"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func Run(ctx context.Context, options RunOptions, input io.Reader, output io.Writer) error {
	if err := RequireRoot(os.Geteuid()); err != nil {
		return err
	}
	if runtime.GOOS != "android" {
		return Reason("android_required")
	}
	if options.UID%100000 < 10000 || options.WorkDir != WorkDirectory || options.Socket != AppFiles(options.UID)+"/singbox-base/command.sock" {
		return Reason("arguments_denied")
	}
	if err := ensureProcessGroup(); err != nil {
		return err
	}
	work, err := openWorkDir()
	if err != nil {
		return err
	}
	defer work.Close()
	lock, err := lockRuntime(work)
	if err != nil {
		return err
	}
	defer lock.Close()
	defer clearSnapshots(work)
	if err = recoverOwned(work); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return ServeLease(ctx, input, output, func(startCtx context.Context, data []byte) (io.Closer, error) {
		app, err := openAppFiles(options.UID)
		if err != nil {
			return nil, err
		}
		defer app.Close()
		adapted, err := AdaptConfig(data, snapshotRules(work, app, options.UID))
		if err != nil {
			return nil, err
		}
		defer clear(adapted)
		if err = validateRuntimeDNS(adapted); err != nil {
			return nil, err
		}
		if err = preflight(); err != nil {
			return nil, err
		}
		if err = writeJournal(work, options.UID); err != nil {
			return nil, err
		}
		coreCtx, coreCancel := context.WithCancel(context.WithoutCancel(startCtx))
		rootCtx := include.Context(coreCtx)
		rootCtx = filemanager.WithDefault(rootCtx, WorkDirectory, WorkDirectory, 0, 0)
		rootCtx = service.ContextWith(rootCtx, &adapter.RootRedirectOwnership{TableName: "etonify", RedirectTable: rootRedirectTable, TProxyTable: rootTProxyTable, RulePriority: rootRedirectPriority})
		started := daemon.NewStartedService(daemon.ServiceOptions{Context: rootCtx, Handler: rootPlatform{}, LogMaxLines: 512})
		host := &rootRuntime{started: started, work: work, cancel: coreCancel}
		startupDone := make(chan struct{})
		go func() {
			select {
			case <-startCtx.Done():
				coreCancel()
			case <-startupDone:
			}
		}()
		if err = started.StartOrReloadService(rootCtx, string(adapted), nil); err != nil {
			close(startupDone)
			host.Close()
			return nil, Reason("runtime_start_failed")
		}
		close(startupDone)
		socket, err := listenSocket(app, options.UID)
		if err != nil {
			host.Close()
			return nil, err
		}
		host.socket = socket
		server := grpc.NewServer(
			grpc.MaxRecvMsgSize(1<<20),
			grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				if !AllowedRPC(info.FullMethod) {
					return nil, status.Error(codes.PermissionDenied, "rpc_denied")
				}
				response, err := handler(ctx, req)
				if err != nil {
					return nil, status.Error(status.Code(err), "request_failed")
				}
				return response, nil
			}),
			grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
				if !AllowedRPC(info.FullMethod) {
					return status.Error(codes.PermissionDenied, "rpc_denied")
				}
				err := handler(srv, stream)
				if err != nil {
					return status.Error(status.Code(err), "request_failed")
				}
				return nil
			}),
		)
		host.grpc = server
		daemon.RegisterStartedServiceServer(server, started)
		daemon.RegisterManagedServiceServer(server, &rootManaged{stop: cancel})
		healthServer := health.NewServer()
		healthServer.SetServingStatus(daemon.StartedService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
		healthServer.SetServingStatus(daemon.ManagedService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
		grpc_health_v1.RegisterHealthServer(server, healthServer)
		go func() {
			if server.Serve(&peerListener{Listener: socket.listener, uid: uint32(options.UID)}) != nil {
				cancel()
			}
		}()
		return host, nil
	})
}

func ensureProcessGroup() error {
	pid := os.Getpid()
	group, err := unix.Getpgid(pid)
	if err != nil {
		return Reason("process_group_failed")
	}
	if group == pid {
		return nil
	}
	if unix.Setpgid(0, 0) != nil {
		return Reason("process_group_failed")
	}
	group, err = unix.Getpgid(pid)
	if err != nil || group != pid {
		return Reason("process_group_failed")
	}
	return nil
}

type rootPlatform struct{}

func (rootPlatform) WriteDebugMessage(string)        {}
func (rootPlatform) ConnectSSHAgent() (int32, error) { return 0, Reason("rpc_denied") }

type rootManaged struct {
	daemon.UnimplementedManagedServiceServer
	stop context.CancelFunc
}

func (r *rootManaged) StopService(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	r.stop()
	return &emptypb.Empty{}, nil
}

type rootRuntime struct {
	once     sync.Once
	started  *daemon.StartedService
	grpc     *grpc.Server
	socket   *socketEndpoint
	work     *os.File
	closeErr error
	cancel   context.CancelFunc
}

func (r *rootRuntime) Close() error {
	r.once.Do(func() {
		if r.grpc != nil {
			done := make(chan struct{})
			go func() { r.grpc.GracefulStop(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				r.grpc.Stop()
				<-done
			}
		}
		if r.socket != nil {
			r.socket.Close()
		}
		if r.started != nil {
			r.started.CloseService()
			r.started.Close()
		}
		if r.cancel != nil {
			r.cancel()
		}
		if r.work != nil {
			r.closeErr = recoverOwned(r.work)
		}
	})
	return r.closeErr
}

type socketEndpoint struct {
	dir      *os.File
	node     *os.File
	listener *net.UnixListener
}

func (s *socketEndpoint) Close() error {
	if s.listener != nil {
		s.listener.Close()
	}
	var expected, actual unix.Stat_t
	if s.node != nil && unix.Fstat(int(s.node.Fd()), &expected) == nil && unix.Fstatat(int(s.dir.Fd()), "command.sock", &actual, unix.AT_SYMLINK_NOFOLLOW) == nil && expected.Dev == actual.Dev && expected.Ino == actual.Ino {
		unix.Unlinkat(int(s.dir.Fd()), "command.sock", 0)
	}
	if s.node != nil {
		s.node.Close()
	}
	return s.dir.Close()
}

func listenSocket(app *os.File, uid int) (*socketEndpoint, error) {
	fd, err := unix.Openat(int(app.Fd()), "singbox-base", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, Reason("socket_directory_denied")
	}
	dir := os.NewFile(uintptr(fd), "singbox-base")
	fail := func(reason Reason) (*socketEndpoint, error) { dir.Close(); return nil, reason }
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || int(stat.Uid) != uid || stat.Mode&0002 != 0 {
		return fail("socket_directory_denied")
	}
	socket := path.Join(fdPath(fd), "command.sock")
	if unix.Fstatat(fd, "command.sock", &stat, unix.AT_SYMLINK_NOFOLLOW) == nil {
		if stat.Mode&unix.S_IFMT != unix.S_IFSOCK || int(stat.Uid) != uid {
			return fail("socket_path_denied")
		}
		conn, dialErr := net.DialTimeout("unix", socket, 200*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return fail("socket_in_use")
		}
		if unix.Unlinkat(fd, "command.sock", 0) != nil {
			return fail("socket_path_denied")
		}
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return fail("socket_listen_failed")
	}
	listener.SetUnlinkOnClose(false)
	endpoint := &socketEndpoint{dir: dir, listener: listener}
	nodeFD, err := unix.Openat(fd, "command.sock", unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		endpoint.Close()
		return nil, Reason("socket_permissions_failed")
	}
	endpoint.node = os.NewFile(uintptr(nodeFD), "command.sock")
	if unix.Fstat(nodeFD, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFSOCK || stat.Uid != 0 || stat.Nlink != 1 || unix.Chmod(fdPath(nodeFD), 0600) != nil || unix.Fchownat(nodeFD, "", uid, uid, unix.AT_EMPTY_PATH) != nil {
		endpoint.Close()
		return nil, Reason("socket_permissions_failed")
	}
	return endpoint, nil
}

type peerListener struct {
	net.Listener
	uid uint32
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			conn.Close()
			continue
		}
		raw, err := unixConn.SyscallConn()
		if err != nil {
			conn.Close()
			continue
		}
		var credentials *unix.Ucred
		err = raw.Control(func(fd uintptr) { credentials, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
		if err != nil || credentials == nil || !PeerAllowed(credentials.Uid, l.uid) {
			conn.Close()
			continue
		}
		return conn, nil
	}
}
