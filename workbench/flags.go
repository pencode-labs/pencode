package workbench

// Absent flags represent the default state and retain no path references.
func togglePencodeFlag[K comparable](flags map[K]bool, key K) map[K]bool {
	if flags[key] {
		delete(flags, key)
	} else {
		if flags == nil {
			flags = make(map[K]bool)
		}
		flags[key] = true
	}
	return flags
}
