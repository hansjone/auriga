package main

import "os"

type sharedProjectedFile struct {
	Body []byte
	UID  uint32
	Mode os.FileMode
}
