package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/syndtr/goleveldb/leveldb/errors"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"io/ioutil"
	"math/big"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/syndtr/goleveldb/leveldb"
)

const (
	NodeStatusOK          = 0
	NodeStatusUnreachable = 1
)

type blockInfo struct {
	num  uint64
	hash common.Hash
}

func (bl *blockInfo) TerminalString() string {
	return fmt.Sprintf("%d [%v]",
		bl.num,
		bl.hash.TerminalString())
}

type Node interface {
	Version() (string, error)
	Name() string
	Status() int
	SetStatus(int)
	UpdateLatest() error
	BlockAt(num uint64, force bool) *blockInfo
	HashAt(num uint64, force bool) common.Hash
	HeadNum() uint64
}

// RPCNode represents a node that is reachable via JSON-rpc
type RPCNode struct {
	rpcCli       *rpc.Client
	ethCli       *ethclient.Client
	version      string
	name         string
	latest       *blockInfo
	chainHistory map[uint64]*blockInfo
	// backend to store hash -> header into
	db     *blockDB
	status int
}

func NewRPCNode(rpcCli *rpc.Client, db *blockDB) *RPCNode {
	ethCli := ethclient.NewClient(rpcCli)
	return &RPCNode{
		rpcCli:       rpcCli,
		ethCli:       ethCli,
		version:      "n/a",
		chainHistory: make(map[uint64]*blockInfo),
		db:           db,
	}
}

func (node *RPCNode) SetStatus(status int) {
	node.status = status
}

func (node *RPCNode) Status() int {
	return node.status
}

func (node *RPCNode) Version() (string, error) {
	var ver string
	ctx := context.Background()
	err := node.rpcCli.CallContext(ctx, &ver, "web3_clientVersion")
	if err == nil {
		parts := strings.Split(ver, "/")
		if len(parts) > 0 {
			node.version = strings.Join(parts[1:], "/")
		}
		node.name = parts[0]
	}
	return ver, err
}

func (node *RPCNode) HeadNum() uint64 {
	if node.latest != nil {
		return node.latest.num
	}
	return 0
}

func (node *RPCNode) Name() string {
	if len(node.name) == 0 {
		node.Version()
	}
	return node.name
}

func (node *RPCNode) UpdateLatest() error {
	bl, err := node.fetchHeader(nil)
	if err != nil {
		return err
	}
	node.latest = bl
	return nil
}

func (node *RPCNode) fetchHeader(num *big.Int) (*blockInfo, error) {
	log.Debug("Doing check", "node", node.name, "requested", num)
	h, err := node.ethCli.HeaderByNumber(context.Background(), num)
	if err != nil {
		log.Error("Blockcheck error", "error", err)
		return nil, err
	}
	if h == nil {
		return nil, fmt.Errorf("Got nil header for, num %d, node %v", num, node.name)
	}
	// Store header to db aswell
	if node.db != nil {
		node.db.add(h.Hash(), h)
	}
	bl := &blockInfo{
		num:  h.Number.Uint64(),
		hash: h.Hash(),
	}
	node.chainHistory[bl.num] = bl
	return bl, nil
}

func (node *RPCNode) BlockAt(num uint64, force bool) *blockInfo {
	if node.latest != nil && node.latest.num < num {
		return nil // that block is future, don't bother
	}
	if !force {
		if bl, ok := node.chainHistory[num]; ok {
			return bl // have it already, don't refetch it
		}
	}
	bl, _ := node.fetchHeader(new(big.Int).SetUint64(num))
	return bl
}

func (node *RPCNode) HashAt(num uint64, force bool) common.Hash {
	if bl := node.BlockAt(num, force); bl != nil {
		return bl.hash
	}
	return common.Hash{}
}

type clientJson struct {
	Version string
	Name    string
	Status  int
}

// Report represents one 'snapshot' of the state of the nodes, where they are at
// in a given time.
type Report struct {
	Cols    []*clientJson
	Rows    map[int][]string
	Numbers []int
	Hashes  []common.Hash
}

func NewReport(headList []int) *Report {
	return &Report{
		Numbers: headList,
		Cols:    nil,
		Rows:    make(map[int][]string),
	}
}

func (r *Report) dedup() {
	// dedup hashes
	var hashMap = make(map[common.Hash]bool)
	for _, h := range r.Hashes {
		hashMap[h] = true
	}
	var hashList []common.Hash
	for k, _ := range hashMap {
		hashList = append(hashList, k)
	}
	r.Hashes = hashList
}

// Print prints the report as a table to the stdout
func (r *Report) Print() {
	var names []string
	for _, c := range r.Cols {
		names = append(names, c.Name)
	}
	hdr := strings.Join(names, " | ")
	fmt.Printf("| number | %v |\n", hdr)
	fmt.Printf("|----")
	for i := 0; i < len(r.Cols); i++ {
		fmt.Printf("|----")
	}
	fmt.Printf("|\n")
	for _, num := range r.Numbers {
		data := strings.Join(r.Rows[num], " | ")
		fmt.Printf("| %d | %v |\n", num, data)
	}
}

// AddToReport adds the given node to the report
func (r *Report) AddToReport(node Node) {
	v, _ := node.Version()
	r.Cols = append(r.Cols,
		&clientJson{
			Version: v,
			Name:    node.Name(),
			Status:  node.Status(),
		},
	)
	for _, num := range r.Numbers {
		row := r.Rows[num]
		block := node.BlockAt(uint64(num), false)
		txt := ""
		if block != nil {
			txt = fmt.Sprintf("0x%x", block.hash)
			r.Hashes = append(r.Hashes, block.hash)
		}
		row = append(row, txt)
		r.Rows[num] = row
	}
	r.dedup()
}

// NodeMonitor monitors a set of nodes, and performs checks on them
type NodeMonitor struct {
	nodes   []Node
	quitCh  chan struct{}
	backend *blockDB
	wg      sync.WaitGroup
}

// NewMonitor creates a new NodeMonitor
func NewMonitor(nodes []Node, db *blockDB) (*NodeMonitor, error) {
	// Do initial healthcheck
	for _, node := range nodes {
		v, err := node.Version()
		if err != nil {
			node.SetStatus(NodeStatusUnreachable)
			log.Error("Error checking version", "error", err)
			return nil, err
		} else {
			node.SetStatus(NodeStatusOK)
		}
		log.Info("RPCNode OK", "version", v)
	}

	nm := &NodeMonitor{
		nodes:   nodes,
		quitCh:  make(chan struct{}),
		backend: db,
	}
	nm.doChecks()
	return nm, nil
}

func (mon *NodeMonitor) Start() {
	mon.wg.Add(1)
	go mon.loop()
}

func (mon *NodeMonitor) Stop() {
	close(mon.quitCh)
	mon.wg.Wait()
}

func (mon *NodeMonitor) loop() {
	defer mon.wg.Done()
	for {
		select {
		case <-mon.quitCh:
			return
		case <-time.After(5 * time.Second):
			mon.doChecks()
		}
	}
}

func (mon *NodeMonitor) doChecks() {

	// We want to cross-check all 'latest' numbers. So if we have
	// node 1: x,
	// node 2: y,
	// node 3: z,
	// Then we want to check the following
	// node 1: (y, z)
	// node 2: (x, z),
	// node 3: (x, y),
	// To figure out if they are on the same chain, or have diverged

	var heads = make(map[uint64]bool)
	for _, node := range mon.nodes {
		err := node.UpdateLatest()
		v, _ := node.Version()
		if err != nil {
			log.Error("Error getting latest", "node", v, "error", err)
		} else {
			num := node.HeadNum()
			log.Info("Latest", "num", num, "node", v)
			heads[num] = true
		}
	}
	// Pair-wise, figure out the splitblocks (if any)
	forPairs(mon.nodes,
		func(a, b Node) {
			highest := a.HeadNum()
			if b.HeadNum() < highest {
				highest = b.HeadNum()
			}
			if a.BlockAt(highest, false) != b.BlockAt(highest, false) {
				split := findSplit(int(highest), a, b)
				log.Info("Split found", "x", a.Name(), "y", b.Name(), "num", split)
				// Point of interest, add split-block and split-block-minus-one to heads
				heads[uint64(split)] = true
				if split > 0 {
					heads[uint64(split-1)] = true
				}
			} else {
				log.Info("Same chain", "x", a.Name(), "y", b.Name(), "highest common", highest)
			}
		},
	)
	var headList []int
	for k, _ := range heads {
		headList = append(headList, int(k))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(headList)))

	r := NewReport(headList)
	for _, node := range mon.nodes {
		r.AddToReport(node)
	}
	r.Print()

	jsd, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		log.Warn("Json marshall fail", "error", err)
		return
	}
	if mon.backend == nil {
		// if there's no backend, this is probably a test.
		// Just return
		return
	}
	if err := ioutil.WriteFile("www/data.json", jsd, 0777); err != nil {
		log.Warn("Failed to write file", "error", err)
		return
	}
	// And now provide relevant hashes
	for _, hash := range r.Hashes {
		hdr := mon.backend.get(hash)
		if hdr == nil {
			log.Warn("Missing header", "hash", hash)
			continue
		}
		fname := fmt.Sprintf("www/hashes/0x%x.json", hash)
		// only write it if it isn't already there
		if _, err := os.Stat(fname); os.IsNotExist(err) {
			data, err := json.MarshalIndent(hdr, "", " ")
			if err != nil {
				log.Warn("Failed to marshall header", "error", err)
				continue
			}
			if err := ioutil.WriteFile(fname, data, 0777); err != nil {
				log.Warn("Failed to write file", "error", err)
				return
			}
		}
	}
}

func ReportNode(node Node, nums []int) {
	v, _ := node.Version()
	fmt.Printf("## %v\n", v)
	for _, num := range nums {
		block := node.BlockAt(uint64(num), false)
		if block != nil {
			fmt.Printf("%d: %v\n", num, block.TerminalString())
		} else {
			fmt.Printf("%d: %v\n", num, "n/a")
		}
	}
}

// For any differences, we want to figure out the split-block.
// Let's say we have:
// node 1: (num1: x)
// node 2: (num1: y)
// Now we need to figure out which block is the first one where they disagreed.
// We do it using a binary search
//
//  Search uses binary search to find and return the smallest index i
//  in [0, n) at which f(i) is true
func findSplit(num int, a Node, b Node) int {
	splitBlock := sort.Search(num, func(i int) bool {
		return a.HashAt(uint64(i), false) != b.HashAt(uint64(i), false)
	})
	return splitBlock
}

// calls 'fn(a, b)' once for each pair in the given list of 'elems'
func forPairs(elems []Node, fn func(a, b Node)) {
	for i := 0; i < len(elems); i++ {
		for j := i + 1; j < len(elems); j++ {
			fn(elems[i], elems[j])
		}
	}
}

type blockDB struct {
	db *leveldb.DB
}

func NewBlockDB() (*blockDB, error) {
	file := "blockDB"
	db, err := leveldb.OpenFile(file, &opt.Options{
		// defaults:
		//BlockCacheCapacity:     8  * opt.MiB,
		//WriteBuffer:            4 * opt.MiB,
	})
	if _, corrupted := err.(*errors.ErrCorrupted); corrupted {
		db, err = leveldb.RecoverFile(file, nil)
	}
	if err != nil {
		return nil, err
	}
	return &blockDB{db}, nil

}

func (db *blockDB) add(key common.Hash, h *types.Header) {
	k := key[:]
	if ok, _ := db.db.Has(k, nil); ok {
		return
	}
	data, err := rlp.EncodeToBytes(h)
	if err != nil {
		panic(fmt.Sprintf("Failed encoding header: %v", err))
	}
	db.db.Put(k, data, nil)
}

func (db *blockDB) get(key common.Hash) *types.Header {
	data, err := db.db.Get(key[:], nil)
	if err != nil {
		return nil
	}
	var h types.Header
	if err = rlp.DecodeBytes(data, &h); err != nil {
		panic(fmt.Sprintf("Failed decoding our own data: %v", err))
	}
	return &h
}
