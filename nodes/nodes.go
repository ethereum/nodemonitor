package nodes

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/eth"
)

const (
	NodeStatusOK          = 0
	NodeStatusUnreachable = 1
)

type blockInfo struct {
	num   uint64
	hash  common.Hash
	pHash common.Hash
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
	LastProgress() int64
	SetStatus(int)
	UpdateLatest() error
	BlockAt(num uint64, force bool) *blockInfo
	HashAt(num uint64, force bool) common.Hash
	HeadNum() uint64
	BadBlocks() []*eth.BadBlockArgs
}

type clientJson struct {
	Version         string
	Name            string
	Status          int
	LastProgress    int64
	BadBlocks       int
	Vulnerabilities []string
}

type badBlockJson struct {
	Client string
	Hash   common.Hash
	RLP    string `json:"-"`
}

// Report represents one 'snapshot' of the state of the nodes, where they are at
// in a given time.
type Report struct {
	Cols      []*clientJson
	Rows      map[int][]string
	Numbers   []int
	Hashes    []common.Hash
	BadBlocks []*badBlockJson
}

func NewReport(headList []int) *Report {
	return &Report{
		Numbers:   headList,
		Cols:      nil,
		Rows:      make(map[int][]string),
		BadBlocks: make([]*badBlockJson, 0),
	}
}

func (r *Report) dedup() {
	// dedup hashes
	var hashMap = make(map[common.Hash]bool)
	for _, h := range r.Hashes {
		hashMap[h] = true
	}
	var hashList []common.Hash
	for k := range hashMap {
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
func (r *Report) AddToReport(node Node, badBlocks map[common.Hash]*badBlockJson, vuln []vulnJson) {
	v, _ := node.Version()
	// Add general node properties
	np := &clientJson{
		Version:      v,
		Name:         node.Name(),
		Status:       node.Status(),
		LastProgress: node.LastProgress(),
		BadBlocks:    len(badBlocks),
	}
	// Add vulnerabilites if applicable
	if len(vuln) != 0 {
		np.Vulnerabilities = make([]string, 0, len(vuln))
		for _, v := range vuln {
			np.Vulnerabilities = append(np.Vulnerabilities, v.Uid)
		}
	}
	r.Cols = append(r.Cols, np)
	// Add bad blocks
	for _, block := range badBlocks {
		r.BadBlocks = append(r.BadBlocks, block)
	}
	// Add hashes
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
