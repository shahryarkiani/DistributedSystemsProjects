package mr

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync/atomic"
	"time"
)

type Coordinator struct {
	// Your definitions here.
	filenames        []string
	nReduce          int
	inProgressMap    []atomic.Bool
	completedMap     []atomic.Bool
	inProgressReduce []atomic.Bool
	completedReduce  []atomic.Bool
	//atomic ints to keep track of assignments
	mapPos           atomic.Int32
	reducePos        atomic.Int32
	nMapCompleted    atomic.Int32
	nReduceCompleted atomic.Int32
}

//This function assigns tasks to a worker requesting work

func (c *Coordinator) AssignTask(_ int, reply *Task) error {

	nMap := len(c.filenames)

	numChecked := 0

	//need a way for worker to say a task is finished

	for int(c.nMapCompleted.Load()) < nMap { //we loop in a circle until all the map tasks are done

		//this isn't strictly necessary, we could use just the compare and swap below to make sure that only one worker gets sent
		//a task, but this just reduces the amount of times two workers try to grab the same task
		mapPos := c.mapPos.Add(1)

		mapPos %= int32(nMap)

		//if we looped all the way around and couldn't find anything, we tell it to wait
		numChecked++
		if numChecked > nMap {
			reply.Code = WAIT
			return nil
		}

		if c.inProgressMap[mapPos].Load() == false && c.completedMap[mapPos].Load() == false { //there's an available task

			if !c.inProgressMap[mapPos].CompareAndSwap(false, true) { //we try to mark the task as in progress for ourself
				continue //someone else grabbed the task
			}

			reply.Code = MAP
			reply.Filename = c.filenames[mapPos]
			reply.TaskNumber = int(mapPos)
			reply.ReduceNumber = c.nReduce

			go func() {
				time.Sleep(time.Duration(10) * time.Second)
				//we mark it as available if it hasn't after 10 seconds so another work can start on it
				if c.completedMap[mapPos].Load() == false {
					c.inProgressMap[mapPos].Store(false)
				}
			}()

			return nil
		}
	}

	numChecked = 0

	for int(c.nReduceCompleted.Load()) < c.nReduce {
		reducePos := c.reducePos.Add(1)

		reducePos %= int32(c.nReduce)

		if numChecked == c.nReduce {
			reply.Code = WAIT
			return nil
		}

		if c.inProgressReduce[reducePos].Load() == false && c.completedReduce[reducePos].Load() == false {
			if !c.inProgressReduce[reducePos].CompareAndSwap(false, true) {
				continue //same pattern as map
			}

			reply.Code = REDUCE
			reply.ReduceNumber = int(reducePos)

			go func() {
				time.Sleep(time.Duration(10) * time.Second)
				if c.completedReduce[reducePos].Load() == false {
					c.inProgressReduce[reducePos].Store(false)
				}
			}()

			return nil
		}
	}

	reply.Code = EXIT

	return nil
}

// workers call this to report that they have finished a specified task
func (c *Coordinator) ReportComplete(task *Task, reply *CompleteReply) error {

	fmt.Println("task completed")

	reply.CompletionCode = DISCARD
	if task.Code == MAP {
		if c.completedMap[task.TaskNumber].CompareAndSwap(false, true) { //are we able to mark it as complete
			//this if statement ensures that we only count a task as completed once

			//the completion code is unused, but maybe i'll come with up something
			reply.CompletionCode = COMMIT
			c.nMapCompleted.Add(1)
		}
	}

	if task.Code == REDUCE {
		if c.completedReduce[task.ReduceNumber].CompareAndSwap(false, true) {
			reply.CompletionCode = COMMIT
			c.nReduceCompleted.Add(1)
		}
	}

	return nil
}

// start a thread that listens for RPCs from worker.go
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
func (c *Coordinator) Done() bool {
	return c.nReduceCompleted.Load() == int32(c.nReduce)
}

// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{}

	c.filenames = files
	c.nReduce = nReduce

	c.mapPos.Store(-1)
	c.reducePos.Store(-1)

	//these arrays keep track of which files have been completed/are in progress
	c.inProgressMap = make([]atomic.Bool, len(files))
	c.completedMap = make([]atomic.Bool, len(files))

	//this array keeps track of which reduce tasks have been completed
	c.inProgressReduce = make([]atomic.Bool, nReduce)
	c.completedReduce = make([]atomic.Bool, nReduce)

	// Your code here.

	c.server()
	return &c
}
