package maclogin

type loginKey struct {
	code  int
	shift bool
}

// These are the physical ANSI keys used by the installer's US login layout.
func passwordKeys(password string) ([]loginKey, bool) {
	rows := []struct {
		characters string
		code       int
	}{
		{"aA", 0}, {"sS", 1}, {"dD", 2}, {"fF", 3}, {"hH", 4}, {"gG", 5}, {"zZ", 6}, {"xX", 7}, {"cC", 8}, {"vV", 9},
		{"bB", 11}, {"qQ", 12}, {"wW", 13}, {"eE", 14}, {"rR", 15}, {"yY", 16}, {"tT", 17},
		{"1!", 18}, {"2@", 19}, {"3#", 20}, {"4$", 21}, {"6^", 22}, {"5%", 23}, {"=+", 24}, {"9(", 25}, {"7&", 26}, {"-_", 27}, {"8*", 28}, {"0)", 29},
		{"]}", 30}, {"oO", 31}, {"uU", 32}, {"[{", 33}, {"iI", 34}, {"pP", 35}, {"lL", 37}, {"jJ", 38}, {"'\"", 39}, {"kK", 40}, {";:", 41}, {"\\|", 42}, {",<", 43}, {"/?", 44}, {"nN", 45}, {"mM", 46}, {".>", 47}, {"`~", 50}, {" ", 49},
	}
	if len(password) < 8 || len(password) > 256 {
		return nil, false
	}
	result := make([]loginKey, 0, len(password))
	for _, ch := range password {
		found := false
		for _, row := range rows {
			for index, candidate := range row.characters {
				if ch == candidate {
					result = append(result, loginKey{row.code, index == 1})
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return result, true
}
