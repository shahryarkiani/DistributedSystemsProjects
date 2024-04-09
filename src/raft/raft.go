package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"

	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	//	"6.5840/labgob"
	"6.5840/labrpc"
)

// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

const FOLLOWER = 0
const CANDIDATE = 1
const LEADER = 2

// A Go object implementing a single Raft peer.
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	state       int //stores whether we are Leader, Candidate or Follower
	currentTerm int
	votedFor    int

	//2b
	commitIndex int
	lastApplied int
	log         []Entry
	nextIndex   []int
	matchIndex  []int

	electionTimeout  time.Time
	heartbeatTimeout time.Time
}

type Entry struct {
	Command interface{}
	Term    int
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	rf.mu.Lock()
	curTerm := rf.currentTerm
	state := rf.state
	rf.mu.Unlock()
	return curTerm, state == LEADER
}

// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

// example RPC handler.
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if args.Term < rf.currentTerm { //old term, we don't give them a vote
		reply.Term = rf.currentTerm // so they can update to the latest term
		reply.VoteGranted = false
		return
	}

	if args.Term > rf.currentTerm { //someone is on a newer term than us
		rf.becomeFollower(args.Term) //so we become a follower
	}

	lastEntry := rf.log[len(rf.log)-1]
	if lastEntry.Term > args.LastLogTerm { //our log is more up to date
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}
	if lastEntry.Term == args.Term && (len(rf.log)-1) > args.LastLogIndex { //our log has more stuff in it at the same term
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}

	if rf.votedFor == -1 || rf.votedFor == args.CandidateId {
		rf.votedFor = args.CandidateId
		reply.Term = rf.currentTerm
		reply.VoteGranted = true
		rf.updateElectionTimeout()
		return
	}

	reply.Term = rf.currentTerm
	reply.VoteGranted = false
}

// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

type AppendEntriesArg struct {
	Term         int
	LeaderId     int
	NewEntries   []Entry
	PrevLogIndex int
	PrevLogTerm  int
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term      int
	Success   bool
	NextIndex int
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArg, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

func (rf *Raft) AppendEntries(args *AppendEntriesArg, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	//fmt.Printf("[%d] follower log is %v\n", rf.me, rf.log)

	reply.NextIndex = -1

	if args.Term < rf.currentTerm {
		reply.Term = rf.currentTerm
		reply.Success = false
		return
	}

	if rf.state == CANDIDATE && args.Term == rf.currentTerm { //We lost the election, someone else is the leader
		//fmt.Println("was candidate, becoming follower")
		rf.becomeFollower(rf.currentTerm)
	}

	if args.Term > rf.currentTerm {
		rf.becomeFollower(args.Term)
	}

	if args.PrevLogIndex >= len(rf.log) {
		reply.Term = rf.currentTerm
		reply.Success = false
		reply.NextIndex = len(rf.log)
		rf.updateElectionTimeout()
		return
	}

	if rf.log[args.PrevLogIndex].Term != args.PrevLogTerm {
		//fmt.Printf("[%d] terms don't match")
		reply.Term = rf.currentTerm
		reply.Success = false

		latestTerm := rf.log[args.PrevLogIndex].Term

		i := len(rf.log)

		for i > 0 {
			i--
			if rf.log[args.PrevLogIndex].Term != latestTerm { //this is where the term switch happened
				reply.NextIndex = i + 1
				break
			}
		}

		rf.updateElectionTimeout()
		return
	}

	//fmt.Printf("[%d] received new logs of %v with prevlogindex of %d\n", rf.me, args.NewEntries, args.PrevLogIndex)

	if len(args.NewEntries) == 0 {
		if args.LeaderCommit > rf.commitIndex {
			rf.commitIndex = args.LeaderCommit
			if (len(rf.log) - 1) < rf.commitIndex {
				rf.commitIndex = len(rf.log) - 1
			}

			//fmt.Printf("[%d] commit index is now %d\n", rf.me, rf.commitIndex)
		}

		reply.Term = rf.currentTerm
		reply.Success = true
		rf.updateElectionTimeout()
		return
	}

	i := 0

	for i = range args.NewEntries {
		if i+args.PrevLogIndex+1 >= len(rf.log) {
			break
		}
		if rf.log[i+args.PrevLogIndex+1].Term != args.NewEntries[i].Term {
			break
		}
	}

	rf.log = rf.log[:(i + args.PrevLogIndex + 1)] //we truncate everything after the conflicting one(if there's no conflict, this truncates nothing)
	args.NewEntries = args.NewEntries[i:]         //We also remove the earlier entries that are already in this log

	rf.log = append(rf.log, args.NewEntries...)

	//fmt.Printf("[%d] Log is now %v\n", rf.me, rf.log)

	if args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = args.LeaderCommit
		if (len(rf.log) - 1) < rf.commitIndex {
			rf.commitIndex = len(rf.log) - 1
		}

		//fmt.Printf("[%d] commit index is now %d\n", rf.me, rf.commitIndex)
	}

	reply.Term = rf.currentTerm
	reply.Success = true
	rf.updateElectionTimeout()
	return
}

func (rf *Raft) doElection() {
	votes := 1
	args := RequestVoteArgs{}
	rf.mu.Lock()

	rf.currentTerm++
	rf.votedFor = rf.me
	rf.state = CANDIDATE

	args.CandidateId = rf.me
	args.Term = rf.currentTerm

	args.LastLogIndex = len(rf.log) - 1
	args.LastLogTerm = rf.log[len(rf.log)-1].Term

	rf.mu.Unlock()

	for i := range rf.peers {

		if i == rf.me {
			continue
		}

		go func(server int) {
			reply := RequestVoteReply{}
			reply.VoteGranted = false
			ok := rf.sendRequestVote(server, &args, &reply)

			if !ok {
				return
			}

			rf.mu.Lock()
			defer rf.mu.Unlock()

			if rf.currentTerm < reply.Term {
				rf.becomeFollower(reply.Term)
				return
			}

			if rf.state != CANDIDATE || rf.currentTerm != args.Term { //is this reply for an old election
				return
			}

			if reply.VoteGranted {

				//fmt.Printf("[%d] Got vote from %d\n", rf.me, server)

				votes++
				if votes > len(rf.peers)/2 {
					//fmt.Printf("[%d] Won election\n", rf.me)
					rf.state = LEADER
					rf.heartbeatTimeout = time.Now()

					for i := range rf.peers { //reinitialize
						rf.nextIndex[i] = len(rf.log)
						rf.matchIndex[i] = 0
					}
					//fmt.Printf("[%d] leader log is %v\n", rf.me, rf.log)
					//fmt.Printf("[%d] nextIndexes are %v]\n", rf.me, rf.nextIndex)
				}
			}
		}(i)
	}

	rf.updateElectionTimeout()

}

func (rf *Raft) doHeartbeats() {
	args := AppendEntriesArg{}
	rf.mu.Lock()

	args.LeaderId = rf.me
	args.Term = rf.currentTerm

	rf.mu.Unlock()

	for i := range rf.peers {

		if i == rf.me {
			continue
		}

		go func(server int) {
			reply := AppendEntriesReply{}
			ok := rf.sendAppendEntries(server, &args, &reply)

			if !ok {
				return
			}

			rf.mu.Lock()
			defer rf.mu.Unlock()

			if reply.Term > rf.currentTerm {
				rf.becomeFollower(reply.Term)
			}
		}(i)
	}

	rf.updateHeartbeatTimeout()

}

func (rf *Raft) ticker() {
	for !rf.killed() {

		rf.mu.Lock()
		curState := rf.state
		electionTimeout := rf.electionTimeout
		heartbeatTimeout := rf.heartbeatTimeout
		rf.mu.Unlock()

		if curState != LEADER && time.Now().After(electionTimeout) {
			//fmt.Printf("[%d] starting election\n", rf.me)
			rf.doElection()
		}

		if curState == LEADER && time.Now().After(heartbeatTimeout) {
			//send hearbeats
			rf.doAppendEntries()
		}

		time.Sleep(time.Duration(50) * time.Millisecond)
	}
}

// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.state = FOLLOWER
	rf.votedFor = -1
	rf.currentTerm = 0
	rf.updateElectionTimeout()

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	rf.log = make([]Entry, 1)
	rf.log[0].Command = nil
	rf.log[0].Term = 0

	rf.nextIndex = make([]int, len(peers))
	rf.matchIndex = make([]int, len(peers))

	rf.lastApplied = 0

	for i := range rf.peers {
		rf.nextIndex[i] = len(rf.log)
		rf.matchIndex[i] = 0
	}

	// start ticker goroutine to start elections
	go rf.ticker()

	go rf.ApplyLogs(applyCh)

	return rf
}

func (rf *Raft) becomeFollower(newterm int) { //should only be called from a locked context
	rf.state = FOLLOWER
	rf.currentTerm = newterm
	rf.votedFor = -1
}

func (rf *Raft) updateElectionTimeout() { //should only be called from a locked context
	ms := 1000 + (rand.Int63() % 750)
	rf.electionTimeout = time.Now().Add(time.Duration(ms) * time.Millisecond)
}

func (rf *Raft) updateHeartbeatTimeout() { //should only be called from a locked context
	ms := 175
	rf.heartbeatTimeout = time.Now().Add(time.Duration(ms) * time.Millisecond)
}

//---------------------------------------------------------------------------

// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
// before you've implemented snapshots, you should pass nil as the
// second argument to persister.Save().
// after you've implemented snapshots, pass the current snapshot
// (or nil if there's not yet a snapshot).
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// raftstate := w.Bytes()
	// rf.persister.Save(raftstate, nil)
}

// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).

}

// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	index := len(rf.log)
	term := rf.currentTerm
	isLeader := rf.state == LEADER

	if isLeader {
		rf.log = append(rf.log, Entry{Command: command, Term: term})
		//fmt.Printf("[%d] Appending new entry of %v\n", rf.me, rf.log[len(rf.log)-1])
		go rf.doAppendEntries()
	}

	rf.mu.Unlock()
	// Your code here (2B).

	return index, term, isLeader
}

func (rf *Raft) ApplyLogs(applyCh chan ApplyMsg) {
	for !rf.killed() {
		rf.mu.Lock()
		appliedMsgs := []ApplyMsg{}

		for rf.commitIndex > rf.lastApplied && rf.lastApplied < len(rf.log)-1 {
			rf.lastApplied++
			appliedMsgs = append(appliedMsgs, ApplyMsg{CommandValid: true, Command: rf.log[rf.lastApplied].Command, CommandIndex: rf.lastApplied})
		}
		rf.mu.Unlock()

		if len(appliedMsgs) > 0 {
			//fmt.Printf("[%d] applied logs %v\n", rf.me, appliedMsgs)
		}

		for _, msg := range appliedMsgs {
			applyCh <- msg
		}

		time.Sleep(time.Duration(50) * time.Millisecond)
	}
}

func (rf *Raft) doAppendEntries() {
	args := []AppendEntriesArg{}
	rf.mu.Lock()

	for i := range rf.peers {

		if i == rf.me {
			args = append(args, AppendEntriesArg{})
			continue
		}

		appendArg := AppendEntriesArg{}
		appendArg.Term = rf.currentTerm
		appendArg.LeaderId = rf.me

		appendArg.PrevLogIndex = rf.nextIndex[i] - 1
		appendArg.PrevLogTerm = rf.log[rf.nextIndex[i]-1].Term

		appendArg.LeaderCommit = rf.commitIndex

		appendArg.NewEntries = []Entry{} //empty if there's nothing to send(can be used for heartbeats)

		if len(rf.log)-1 >= rf.nextIndex[i] {
			appendArg.NewEntries = rf.log[rf.nextIndex[i]:] //we send everything that the follower doesn't have yet
		}

		if len(appendArg.NewEntries) > 0 {
			//fmt.Printf("[%d] Sending logs %v to %d\n", rf.me, appendArg.NewEntries, i)
		}

		args = append(args, appendArg)

	}

	rf.mu.Unlock()

	for idx := range rf.peers {

		if idx == rf.me {
			continue
		}

		go func(server int) {
			reply := AppendEntriesReply{}
			reply.Success = false
			ok := rf.sendAppendEntries(server, &args[server], &reply)

			if !ok {
				return
			}

			rf.mu.Lock()
			defer rf.mu.Unlock()
			//fmt.Printf("[%d] got response from %d\n", rf.me, server)
			if rf.currentTerm < reply.Term {
				//fmt.Printf("[%d] becoming follower\n", rf.me)
				rf.becomeFollower(reply.Term)
				return
			}

			if rf.state != LEADER {
				return
			}

			if !reply.Success {
				rf.nextIndex[server]--
				//it'll be resent later
				if reply.NextIndex != -1 {
					rf.nextIndex[server] = reply.NextIndex
				}
				return
			}

			newNext := args[server].PrevLogIndex + 1 + len(args[server].NewEntries)
			newMatch := args[server].PrevLogIndex + len(args[server].NewEntries)
			if newNext > rf.nextIndex[server] {
				rf.nextIndex[server] = newNext
			}
			if newMatch > rf.matchIndex[server] {
				rf.matchIndex[server] = newMatch
			}

			//fmt.Printf("[%d] commitIndex is %d\n", rf.me, rf.commitIndex)
			//fmt.Printf("[%d] matchIndexes are %v\n", rf.me, rf.matchIndex)

			//can we push commit index forward?

			lowestMatchIndex := math.MaxInt
			numAboveCommit := 0

			for i := range rf.peers { //check for a majority
				if i == rf.me {
					continue
				}

				if rf.matchIndex[i] > rf.commitIndex {
					numAboveCommit++
				}

				if rf.matchIndex[i] > rf.commitIndex && rf.matchIndex[i] < lowestMatchIndex {
					lowestMatchIndex = rf.matchIndex[i]
				}
			}

			//fmt.Printf("[%d] numAboveCommit is %d and lowestMatchIndex is %d\n", rf.me, numAboveCommit, lowestMatchIndex)

			if numAboveCommit >= (len(rf.peers)-1)/2 && rf.log[lowestMatchIndex].Term == rf.currentTerm {
				rf.commitIndex = lowestMatchIndex
				//fmt.Printf("[%d] LEADER, updated commit index to %d\n", rf.me, rf.commitIndex)
			}

		}(idx)
	}

	rf.updateHeartbeatTimeout()

}
