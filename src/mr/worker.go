package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"log"
	"net/rpc"
	"os"
	"sort"
	"time"
)

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.

	//The worker asks the coordinator for work until it gets an exit
	//The coordinator gives 4 possible responses
	//Map -> a file name and map task number
	//Reduce -> A reduce task number
	//Wait -> just tells the worker to go back to sleep
	//Exit -> tells the worker that it should exit

	// uncomment to send the Example RPC to the coordinator.
	// CallExample()

	task := RequestTask()

	for task.Code != EXIT {

		fmt.Printf("Got code %v task %v for file %v\n", task.Code, task.TaskNumber, task.Filename)

		if task.Code == WAIT {
			time.Sleep(time.Duration(300) * time.Millisecond)
			task = RequestTask()
			continue
		} else if task.Code == MAP {
			//handle map
			Map(task, mapf)
		} else if task.Code == REDUCE {
			//handle reduce
		}

		ReportDone(task)

		task = RequestTask()
	}

	fmt.Printf("Everything is done according to coordinator, exiting\n")
}

func Map(task Task, mapf func(string, string) []KeyValue) {
	filename := task.Filename
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open %v", filename)
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", filename)
	}
	file.Close()
	keyvalues := mapf(filename, string(content))

	intermediate := make([][]KeyValue, task.ReduceNumber)

	for i, _ := range intermediate {
		intermediate[i] = []KeyValue{}
	}

	for _, kv := range keyvalues {
		index := ihash(kv.Key) % task.ReduceNumber
		intermediate[index] = append(intermediate[index], kv)
	}

	for i, _ := range intermediate {
		sort.Sort(ByKey(intermediate[i]))
	}

	for i := 0; i < task.ReduceNumber; i++ {
		output, err := os.CreateTemp(".", "WORKERTMP")

		if err != nil {
			fmt.Println(err.Error())
			fmt.Println("Could not create temporary file")
			os.Exit(1)
		}

		enc := json.NewEncoder(output)

		for _, kv := range intermediate[i] {
			err := enc.Encode(&kv)
			if err != nil {
				fmt.Println("Error when writing to temporary file")
			}
		}

		finalfilename := fmt.Sprintf("mr-%d-%d", task.TaskNumber, i)

		os.Rename(output.Name(), finalfilename)
	}
}

func RequestTask() Task {
	reply := Task{}

	ok := call("Coordinator.AssignTask", 0, &reply)
	if ok {
		return reply
	} else {
		fmt.Println("call failed to assign task")
		return reply
	}
}

// we tell the coordinator that we're done, returns true if we should commit our changes
func ReportDone(task Task) bool {
	reply := CompleteReply{}

	ok := call("Coordinator.ReportComplete", &task, &reply)
	if ok {
		return reply.CompletionCode == COMMIT
	} else {
		fmt.Println("call failed to report done")
		return false
	}
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
