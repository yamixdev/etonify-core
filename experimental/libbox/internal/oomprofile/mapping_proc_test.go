package oomprofile

import "testing"

func TestParseProcSelfMaps(t *testing.T) {
	data := []byte("00400000-0040b000 r-xp 00000000 fc:01 787766 /bin/etonify\n" +
		"0060a000-0060b000 r--p 0000a000 fc:01 787766 /bin/etonify\n" +
		"7f000000-7f001000 r-xp 00001000 fc:01 42 /tmp/plugin.so (deleted)\n" +
		"7ffc0000-7ffc1000 r-xp 00000000 00:00 0 [vdso]\n" +
		"7ffd0000-7ffd1000 r-xp 00000000 00:00 0\n")

	type mapping struct {
		low, high, offset uint64
		file, buildID     string
	}
	var mappings []mapping
	parseProcSelfMaps(data, func(low, high, offset uint64, file, buildID string) {
		mappings = append(mappings, mapping{low, high, offset, file, buildID})
	})

	if len(mappings) != 3 {
		t.Fatalf("unexpected executable mapping count: %d", len(mappings))
	}
	if mappings[0] != (mapping{0x00400000, 0x0040b000, 0, "/bin/etonify", ""}) {
		t.Fatalf("unexpected executable mapping: %+v", mappings[0])
	}
	if mappings[1].file != "/tmp/plugin.so" || mappings[1].offset != 0x1000 {
		t.Fatalf("deleted suffix was not normalized: %+v", mappings[1])
	}
	if mappings[2].file != "[vdso]" {
		t.Fatalf("inode-zero named mapping was lost: %+v", mappings[2])
	}
}
