package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import (
	"os"
	"strconv"
)

//
// example to show how to declare the arguments
// and reply for an RPC.
//

const MAP = 0
const REDUCE = 1
const WAIT = 2
const EXIT = 3

type Task struct {
	Code         byte
	Filename     string
	TaskNumber   int
	ReduceNumber int
}

const COMMIT = 0
const DISCARD = 1

type CompleteReply struct {
	CompletionCode byte
}

// Add your RPC definitions here.

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/5840-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
