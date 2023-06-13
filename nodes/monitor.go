package nodes

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/errors"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// NodeMonitor monitors a set of nodes, and performs checks on them
type NodeMonitor struct {
	nodes           []Node
	badBlocks       map[common.Hash]*badBlockJson
	quitCh          chan struct{}
	backend         *blockDB
	wg              sync.WaitGroup
	reloadInterval  time.Duration
	lastClean       time.Time
	lastBadBlocks   time.Time
	forkHeightCache []int
	chainName       string
	// used for testing
	lastReport *Report
}

// NewMonitor creates a new NodeMonitor
func NewMonitor(nodes []Node, db *blockDB, reload time.Duration, chainName string) (*NodeMonitor, error) {
	// Do initial healthcheck
	for _, node := range nodes {
		log.Info("Checking health", "node", node.Name())
		v, err := node.Version()
		if err != nil {
			node.SetStatus(NodeStatusUnreachable)
			log.Error("Error checking version", "error", err)
		} else {
			node.SetStatus(NodeStatusOK)
		}
		log.Info("RemoteNode OK", "version", v)
	}
	if reload == 0 {
		reload = 10 * time.Second
	}

	nm := &NodeMonitor{
		nodes:          nodes,
		badBlocks:      make(map[common.Hash]*badBlockJson),
		quitCh:         make(chan struct{}),
		backend:        db,
		reloadInterval: reload,
		chainName:      chainName,
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
	mon.backend.db.Close()
}

func (mon *NodeMonitor) loop() {
	defer mon.wg.Done()
	for {
		select {
		case <-mon.quitCh:
			return
		case <-time.After(mon.reloadInterval):
			mon.doChecks()
		}
	}
}

func (mon *NodeMonitor) doChecks() {
	var activeNodes []Node

	doneCh := make(chan Node)
	for _, node := range mon.nodes {
		go func(node Node) {
			defer func() {
				doneCh <- node
			}()
			err := node.UpdateLatest()
			v, _ := node.Version()
			if err != nil {
				log.Error("Error getting latest", "node", v, "error", err)
				node.SetStatus(NodeStatusUnreachable)
				return
			}
			node.SetStatus(NodeStatusOK)
		}(node)
	}
	// Wait for them to report back
	for i := 0; i < len(mon.nodes); i++ {
		node := <-doneCh
		if node.Status() == NodeStatusUnreachable {
			continue
		}
		activeNodes = append(activeNodes, node)
	}
	// Sort the activeNodes by name
	sort.Slice(activeNodes, func(i, j int) bool {
		return activeNodes[i].Name() < activeNodes[j].Name()
	})

	// Pair-wise, figure out the splitblocks (if any)
	heads := mon.findSplits(activeNodes)
	var headList []int
	for k := range heads {
		headList = append(headList, int(k))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(headList)))
	// cache headlist for next round
	mon.forkHeightCache = headList

	// create a new report
	r := NewReport(headList, mon.chainName)
	for _, n := range mon.nodes {
		// check vulnerability reports
		vuln, err := checkNode(n)
		if err != nil {
			log.Info("Error while checking for vulnerabilities", "error", err)
		}
		r.AddToReport(n, vuln)
	}
	// Update bad blocks
	mon.checkBadBlocks()
	r.addBadBlocks(mon.badBlocks)

	if mon.backend == nil {
		// if there's no backend, this is probably a test.
		// Just set it and return
		mon.lastReport = r
		return
	}
	jsd, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		log.Warn("Json marshall fail", "error", err)
		return
	}
	if err := ioutil.WriteFile("www/data.json", jsd, 0777); err != nil {
		log.Warn("Failed to write file", "error", err)
		return
	}
	mon.provideHashes(r)
	mon.provideBadBlocks()
	mon.provideVulns()
}

func (mon *NodeMonitor) checkBadBlocks() {
	if time.Since(mon.lastBadBlocks) < time.Minute {
		return
	}
	mon.lastBadBlocks = time.Now()
	for _, node := range mon.nodes {
		blocks := getBadBlocks(node)
		for i := range blocks {
			hash := blocks[i].Hash
			info := mon.badBlocks[hash]
			if info == nil {
				mon.badBlocks[hash] = blocks[i]
				log.Info("Added (new) bad block", "hash", hash)
				continue
			}
			// it's already reported. Add this client name to it (if not set already)
			var alreadyThere bool
			for _, c := range info.Clients {
				if c == node.Name() {
					alreadyThere = true
				}
			}
			if !alreadyThere {
				info.Clients = append(info.Clients, node.Name())
			}
		}
	}
}

func (mon *NodeMonitor) findSplits(activeNodes []Node) map[uint64]bool {
	t0 := time.Now()
	var heads = make(map[uint64]bool)
	var cache = make(map[common.Hash]int)
	var logCtx []interface{}

	var distinctNodes []Node

	for i, node := range activeNodes {
		block := node.BlockAt(node.HeadNum(), false)
		ver, _ := node.Version()
		heads[block.num] = true
		if _, ok := cache[block.hash]; !ok {
			cache[block.hash] = i
			distinctNodes = append(distinctNodes, node)
		}

		logCtx = append(logCtx, fmt.Sprintf("%d-num", i), block.num)
		logCtx = append(logCtx, fmt.Sprintf("%d-name", i), ver)
	}
	log.Info("Latest", logCtx...)
	t1 := time.Now()
	// splitSize is the max amount of blocks in any chain not accepted by all nodes.
	// If one node is simply 'behind' that does not count, since it has yet
	// to accept the canon chain
	var splitSize int64
	// We want to cross-check all 'latest' numbers. So if we have
	// node 1: x,
	// node 2: y,
	// node 3: z,
	// Then we want to check the following
	// node 1: (y, z)
	// node 2: (x, z),
	// node 3: (x, y),
	// To figure out if they are on the same chain, or have diverged
	var headMu sync.Mutex
	forPairs(distinctNodes,
		func(a, b Node) {
			log.Info("Cross-checking", "a", a.Name(), "b", b.Name())
			highest := a.HeadNum()
			if b.HeadNum() < highest {
				highest = b.HeadNum()
			}
			// At the number where both nodes have blocks, check if the two
			// blocks are identical
			ha := a.BlockAt(highest, false)
			if ha == nil {
				// Yeah this actually _does_ happen, see https://github.com/NethermindEth/nethermind/issues/2306
				log.Error("Node seems to be missing blocks", "name", a.Name(), "number", highest)
				return
			}
			hb := b.BlockAt(highest, false)
			if hb == nil {
				log.Error("Node seems to be missing blocks", "name", b.Name(), "number", highest)
				return
			}
			if ha.hash == hb.hash {
				return
			}
			// They appear to have diverged
			split := findSplit(mon.forkHeightCache, int(highest), a, b)
			splitLength := int64(int(highest) - split)
			if splitSize < splitLength {
				splitSize = splitLength
			}
			log.Info("Split found", "x", a.Name(), "y", b.Name(), "num", split, "xHash", ha.hash, "yHash", hb.hash)
			// Point of interest, add split-block and split-block-minus-one to heads
			headMu.Lock()
			defer headMu.Unlock()
			heads[uint64(split)] = true
			if split > 0 {
				heads[uint64(split-1)] = true
			}
		},
	)
	t2 := time.Now()
	log.Info("Update complete", "head-update", t1.Sub(t0), "forkcheck", t2.Sub(t1))
	metrics.GetOrRegisterGauge("chain/split", registry).Update(int64(splitSize))
	return heads
}

func (mon *NodeMonitor) provideHashes(r *Report) {
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
				log.Warn("Failed to marshal header", "error", err)
				continue
			}
			if err := ioutil.WriteFile(fname, data, 0777); err != nil {
				log.Warn("Failed to write file", "error", err)
				return
			}
		}
	}
	if time.Since(mon.lastClean) > time.Minute*10 {
		cleanHashes("www/hashes/", r.Hashes)
		mon.lastClean = time.Now()
	}
}

func (mon *NodeMonitor) provideVulns() {
	for _, v := range checkCache {
		fname := fmt.Sprintf("www/vulns/%v.json", v.Uid)
		// only write it if it isn't already there
		if _, err := os.Stat(fname); os.IsNotExist(err) {
			data, err := json.MarshalIndent(v, "", " ")
			if err != nil {
				log.Warn("Failed to marshal vulnerability", "error", err)
				continue
			}
			if err := ioutil.WriteFile(fname, data, 0777); err != nil {
				log.Warn("Failed to write file", "error", err)
				return
			}
		}
	}
}

// provideBadBlocks stores (newly found) bad block to disk
func (mon *NodeMonitor) provideBadBlocks() {
	for hash, block := range mon.badBlocks {
		fname := fmt.Sprintf("www/badblocks/0x%x.json", hash)
		// only write it if it isn't already there
		if _, err := os.Stat(fname); !os.IsNotExist(err) {
			continue
		}
		var data []byte
		var err error
		// try to retrieve header from backend
		if bl := mon.backend.get(block.Hash); bl != nil {
			type Header types.Header
			data, err = json.MarshalIndent(struct {
				Header
				RLP string `json:"rlp"`
			}{
				Header: Header(*bl),
				RLP:    block.RLP,
			}, "", " ")
			if err != nil {
				log.Warn("Failed to marshal header", "error", err)
			}
		}
		if len(data) == 0 {
			// block not found in backend, print what we know
			b := block
			data, err = json.MarshalIndent(b, "", " ")
			if err != nil {
				log.Warn("Failed to marshal header", "error", err)
				continue
			}
		}
		if err := ioutil.WriteFile(fname, data, 0777); err != nil {
			log.Warn("Failed to write file", "error", err)
			return
		}
	}
}

func getBadBlocks(node Node) []*badBlockJson {
	badBlocks := node.BadBlocks()
	var blockJSON []*badBlockJson

	var encBlock types.Block
	for _, block := range badBlocks {
		info := &badBlockJson{
			Clients: []string{node.Name()},
			Hash:    block.Hash,
			RLP:     block.RLP,
		}
		if err := rlp.DecodeBytes(common.FromHex(block.RLP), &encBlock); err == nil {
			info.Number = encBlock.Number()
			pHash := encBlock.ParentHash()
			info.ParentHash = &pHash
			time := encBlock.Time()
			info.Time = &time
			info.Extra = encBlock.Extra()
			coinbase := encBlock.Coinbase()
			info.Coinbase = &coinbase
			root := encBlock.Root()
			info.Root = &root
		} else {
			log.Warn("Error decoding block", "err", err)
		}
		blockJSON = append(blockJSON, info)
	}
	return blockJSON
}

var fileRe = regexp.MustCompile(`^0x([a-z0-9.]+).json$`)

func cleanHashes(hashdir string, skip []common.Hash) {
	// And clean out old hashes-files
	files, err := ioutil.ReadDir(hashdir)

	if err != nil {
		log.Warn("Cleaning hashes failed", "error", err)
		return
	}
	skipMap := make(map[common.Hash]bool)
	for _, h := range skip {
		skipMap[h] = true
	}
	count := 0
	for _, f := range files {
		match := fileRe.FindStringSubmatch(f.Name())
		if match == nil {
			continue
		}
		hash := common.HexToHash(match[1])
		if _, ok := skipMap[hash]; ok {
			continue
		}
		os.Remove(filepath.Join(hashdir, f.Name()))
		count++
	}
	log.Info("Cleaned hashes", "files", count)
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
func findSplit(forkHeightCache []int, num int, a Node, b Node) int {
	for i := len(forkHeightCache) - 1; i > 0; i-- {
		head := forkHeightCache[i]
		if a.HashAt(uint64(head), false) != b.HashAt(uint64(head), false) {
			// they differ at 'head'
			if head == 0 || a.HashAt(uint64(head-1), false) == b.HashAt(uint64(head-1), false) {
				// ... and parent of 'head' is identical (or 'head' is genesis)
				return head
			}
		}
	}
	// If the split has not occured yet, we only need to search the remaining space
	left := 0
	if len(forkHeightCache) > 0 {
		left = forkHeightCache[0]
	}
	splitBlock := sort.Search(num-left, func(i int) bool {
		return a.HashAt(uint64(left+i), false) != b.HashAt(uint64(left+i), false)
	})
	return splitBlock + left
}

// calls 'fn(a, b)' once for each pair in the given list of 'elems'
func forPairs(elems []Node, fn func(a, b Node)) {

	pairs := make(chan [2]Node)
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pair := range pairs {
				fn(pair[0], pair[1])
			}
		}()
	}
	for i := 0; i < len(elems); i++ {
		for j := i + 1; j < len(elems); j++ {
			pairs <- [2]Node{elems[i], elems[j]}
		}
	}
	close(pairs)
	wg.Wait()
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
