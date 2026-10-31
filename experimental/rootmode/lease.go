package rootmode

import (
	"bufio"
	"context"
	"io"
)

func ServeLease(ctx context.Context, input io.Reader, output io.Writer, start func(context.Context, []byte) (io.Closer, error)) (result error) {
	input = bufio.NewReaderSize(input, 64<<10)
	type frame struct {
		data []byte
		err  error
	}
	frames := make(chan frame, 1)
	go func() { data, err := ReadConfigFrame(input, MaxConfigBytes); frames <- frame{data, err} }()
	var data []byte
	var err error
	select {
	case <-ctx.Done():
		return nil
	case received := <-frames:
		data, err = received.data, received.err
	}
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		clear(data)
		return nil
	}
	runtime, err := start(ctx, data)
	clear(data)
	if err != nil {
		if reason, ok := err.(Reason); ok {
			return reason
		}
		return Reason("runtime_start_failed")
	}
	defer func() {
		if err := runtime.Close(); err != nil && result == nil {
			result = Reason("cleanup_failed")
		}
	}()
	if _, err = io.WriteString(output, "{\"ready\":true,\"protocol\":1}\n"); err != nil {
		return Reason("ready_write_failed")
	}
	ended := make(chan struct{})
	go func() { var one [1]byte; _, _ = input.Read(one[:]); close(ended) }()
	select {
	case <-ctx.Done():
	case <-ended:
	}
	return nil
}
