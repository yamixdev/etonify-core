package rootmode

type RunOptions struct {
	UID     int
	Socket  string
	WorkDir string
}

const WorkDirectory = "/data/adb/etonify/runtime"
