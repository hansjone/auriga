package main

import "os"

// Server installations run on Linux. Keep Windows pure adapter fixtures usable;
// do not claim that portable FileMode proves a Windows private ACL.
func sharedSeedMetadata(info os.FileInfo) bool { return info.Mode().IsRegular() }
