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
	nodes          []Node
	badBlocks      []map[common.Hash]*badBlockJson
	quitCh         chan struct{}
	backend        *blockDB
	wg             sync.WaitGroup
	reloadInterval time.Duration
	lastClean      time.Time
	lastBadBlocks  time.Time
}

// NewMonitor creates a new NodeMonitor
func NewMonitor(nodes []Node, db *blockDB, reload time.Duration) (*NodeMonitor, error) {
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
	badBlocks := make([]map[common.Hash]*badBlockJson, len(nodes))
	for i := range badBlocks {
		badBlocks[i] = make(map[common.Hash]*badBlockJson)
	}

	nm := &NodeMonitor{
		nodes:          nodes,
		badBlocks:      badBlocks,
		quitCh:         make(chan struct{}),
		backend:        db,
		reloadInterval: reload,
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
		case <-time.After(mon.reloadInterval):
			mon.doChecks()
		}
	}
}

func (mon *NodeMonitor) doChecks() {

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

	var heads = make(map[uint64]bool)
	var activeNodes []Node
	var logCtx []interface{}

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
		num := node.HeadNum()
		ver, _ := node.Version()
		heads[num] = true
		logCtx = append(logCtx, fmt.Sprintf("%d-num", i), num)
		logCtx = append(logCtx, fmt.Sprintf("%d-name", i), ver)
	}

	log.Info("Latest", logCtx...)

	// Pair-wise, figure out the splitblocks (if any)
	var headMu sync.Mutex
	forPairs(activeNodes,
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
			split := findSplit(int(highest), a, b)
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
	metrics.GetOrRegisterGauge("chain/split", registry).Update(int64(splitSize))
	var headList []int
	for k := range heads {
		headList = append(headList, int(k))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(headList)))

	// create a new report
	r := NewReport(headList)
	for i, n := range mon.nodes {
		// check vulnerability reports
		vuln, err := checkNode(n)
		if err != nil {
			log.Info("Error while checking for vulnerabilities", "error", err)
		}
		r.AddToReport(n, mon.badBlocks[i], vuln)
	}
	// Add bad blocks every minute
	if time.Since(mon.lastBadBlocks) > time.Minute {
		for i := range mon.nodes {
			blocks := getBadBlocks(mon.nodes[i])
			for _, b := range blocks {
				mon.badBlocks[i][b.Hash] = b
			}
		}
		mon.lastBadBlocks = time.Now()
	}

	jsd, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		log.Warn("Json marshall fail", "error", err)
		return
	}
	if mon.backend == nil {
		// if there's no backend, this is probably a test.
		// Just print and return
		r.Print()
		fmt.Println(string(jsd))
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

func (mon *NodeMonitor) provideBadBlocks() {
	for i := range mon.badBlocks {
		for _, block := range mon.badBlocks[i] {
			fname := fmt.Sprintf("www/badblocks/0x%x.json", block.Hash)
			// only write it if it isn't already there
			if _, err := os.Stat(fname); os.IsNotExist(err) {
				var data []byte
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
					type msBadBlockJSON struct {
						Client string      `json:"client"`
						Hash   common.Hash `json:"hash"`
						RLP    string      `json:"rlp"`
					}
					b := msBadBlockJSON{
						Client: block.Client,
						Hash:   block.Hash,
						RLP:    block.RLP,
					}
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
	}
}

func getBadBlocks(node Node) []*badBlockJson {
	badBlocks := node.BadBlocks()
	var blockJSON []*badBlockJson
	// Add bad blocks to report
	for _, block := range badBlocks {
		blockJSON = append(blockJSON,
			&badBlockJson{
				Client: node.Name(),
				Hash:   block.Hash,
				RLP:    block.RLP,
			},
		)
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
func findSplit(num int, a Node, b Node) int {
	splitBlock := sort.Search(num, func(i int) bool {
		return a.HashAt(uint64(i), false) != b.HashAt(uint64(i), false)
	})
	return splitBlock
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
