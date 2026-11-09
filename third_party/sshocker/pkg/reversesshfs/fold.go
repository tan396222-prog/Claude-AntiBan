package reversesshfs

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// FoldName is s as a case-insensitive or normalization-insensitive file system compares it, or wider:
// two names such a file system takes as one entry have the same FoldName.
//
// It drops the default-ignorable code points (HFS+ and the casefold feature of ext4, f2fs and tmpfs ignore
// them; git's utf8.c has HFS+'s list), canonically decomposes (APFS, HFS+ and casefold ignore
// normalization) and fully case-folds (APFS folds "ß" as "ss"). Dotless "ı" is "i", as in NTFS's
// upcase table. Names that differ only in these ways are the same name here, even where the
// host's file system keeps them apart: that only makes more paths protected.
//
// Not covered: a code point the file system's Unicode tables know and Go's do not (the kernel's are older),
// and a volume with a customized NTFS upcase table. On Windows the protection does not rest on this
// alone: handles are checked by their normalized names.
func FoldName(s string) string {
	if isPlainASCII(s) {
		return strings.ToLower(s)
	}
	s = strings.Map(foldRune, s)
	s = norm.NFD.String(cases.Fold().String(norm.NFD.String(s)))
	return strings.Map(foldRune, s)
}

func isPlainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// foldRune drops what is ignorable in a name, and maps dotless i.
func foldRune(r rune) rune {
	switch {
	case r == 'ı':
		return 'i'
	case r < 0x80:
		return r
	case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Variation_Selector, r), unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r):
		return -1
	}
	return r
}
