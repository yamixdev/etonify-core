package rootmode

func workDirComponentAllowed(part string, uid, gid, mode uint32) bool {
	if mode&0002 != 0 {
		return false
	}
	switch part {
	case "data":
		// Android owns /data as system:system, normally with mode 0771.
		return (uid == 0 || uid == 1000) && (mode&0020 == 0 || gid == 0 || gid == 1000)
	case "adb":
		return uid == 0 && (mode&0020 == 0 || gid == 0)
	case "etonify", "runtime":
		return uid == 0 && mode&0077 == 0
	default:
		return false
	}
}
