package oomprofile

import (
	"bytes"
	"strconv"
	"strings"
)

var (
	procMapSpace   = []byte(" ")
	procMapNewline = []byte("\n")
)

// parseProcSelfMaps extracts executable mappings without depending on private
// runtime/pprof symbols. Build IDs are optional in pprof mappings; omitting
// them keeps this parser stable across Go toolchain updates.
func parseProcSelfMaps(data []byte, addMapping func(lo, hi, offset uint64, file, buildID string)) {
	var line []byte
	next := func() []byte {
		field, remaining, _ := bytes.Cut(line, procMapSpace)
		line = bytes.TrimLeft(remaining, " ")
		return field
	}

	for len(data) > 0 {
		line, data, _ = bytes.Cut(data, procMapNewline)
		lowText, highText, ok := strings.Cut(string(next()), "-")
		if !ok {
			continue
		}
		low, err := strconv.ParseUint(lowText, 16, 64)
		if err != nil {
			continue
		}
		high, err := strconv.ParseUint(highText, 16, 64)
		if err != nil {
			continue
		}
		permissions := next()
		if len(permissions) < 4 || permissions[2] != 'x' {
			continue
		}
		offset, err := strconv.ParseUint(string(next()), 16, 64)
		if err != nil {
			continue
		}
		next()          // device
		inode := next() // inode
		if line == nil {
			continue
		}
		file := strings.TrimSuffix(string(line), " (deleted)")
		if bytes.Equal(inode, []byte("0")) && file == "" {
			continue
		}
		addMapping(low, high, offset, file, "")
	}
}
