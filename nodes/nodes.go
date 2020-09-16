package nodes

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/rpc"
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

	headGauge metrics.Gauge
}

func NewRPCNode(name string, rpcCli *rpc.Client, db *blockDB) *RPCNode {
	ethCli := ethclient.NewClient(rpcCli)
	gaugeName := fmt.Sprintf("head/%v", name)
	return &RPCNode{
		rpcCli:       rpcCli,
		ethCli:       ethCli,
		name:         name,
		version:      "n/a",
		chainHistory: make(map[uint64]*blockInfo),
		db:           db,
		headGauge:    metrics.GetOrRegisterGauge(gaugeName, registry),
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
	return node.name
}

func (node *RPCNode) UpdateLatest() error {
	bl, err := node.fetchHeader(nil)
	if err != nil {
		return err
	}
	node.latest = bl
	node.headGauge.Update(int64(bl.num))
	return nil
}

func (node *RPCNode) fetchHeader(num *big.Int) (*blockInfo, error) {
	log.Debug("Doing check", "node", node.name, "requested", num)
	h, err := node.ethCli.HeaderByNumber(context.Background(), num)
	if err != nil {
		//log.Error("Blockcheck error", "error", err)
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
