//go:build linux || darwin || windows

package reversesshfs

import (
	"testing"
)

func TestFoldNameIgnorables(t *testing.T) {
	// Spellings that a casefold ext4, f2fs or tmpfs, or HFS+, takes as ".git".
	for _, r := range []rune{
		0x200b, 0x200c, 0x200d, 0x200e, 0x200f, 0x202a, 0x202e, 0x2060, 0x2061, 0x2064, 0x206a, 0x206f, 0xfeff,
		0xad, 0x34f, 0x61c, 0x115f, 0x1160, 0x17b4, 0x17b5, 0x180b, 0x180e, 0x180f, 0x3164, 0xfe00, 0xfe0f, 0xffa0,
		0xfff0, 0xe0001, 0xe0020, 0xe0100, 0xe01ef,
	} {
		s := string(r)
		for _, name := range []string{".g" + s + "it", ".gi" + s + "t", ".git" + s, s + ".git", ".GIT" + s} {
			if !sameName(name, ".git") {
				t.Errorf("%+q should match .git", name)
			}
		}
	}
	// Dotless i is "i" in NTFS's upcase table; long s folds to s.
	if !sameName(".gıt", ".git") || !sameName("ſrc", "src") {
		t.Error("dotless i or long s")
	}
	for _, name := range []string{".gt", ".gitt", ".gi.t", ".ǵit", ".g it"} {
		if sameName(name, ".git") {
			t.Errorf("%+q should not match .git", name)
		}
	}
}

func FuzzFoldIgnorables(f *testing.F) {
	for _, s := range []string{".git", ".g​it", "Aㅤb", "é", "é"} {
		f.Add(s, uint8(1))
	}
	f.Fuzz(func(t *testing.T, s string, at uint8) {
		// An ignorable code point anywhere changes nothing.
		i := int(at) % (len(s) + 1)
		for i > 0 && i < len(s) && s[i]&0xc0 == 0x80 {
			i--
		}
		if got, want := FoldName(s[:i]+"​️ㅤ"+s[i:]), FoldName(s); got != want {
			t.Errorf("FoldName(%q with ignorables at %d) = %q, want %q", s, i, got, want)
		}
	})
}
