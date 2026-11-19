package storage

// Directory fsync is a Linux production guarantee; Windows fixtures exercise
// the same durable bbolt reservation and retry rules without a mounted ext4 FS.
func syncArchiveDirectory(string) error { return nil }
