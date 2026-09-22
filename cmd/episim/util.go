package main

import "path/filepath"

func filepathJoin(elem ...string) string { return filepath.Join(elem...) }
