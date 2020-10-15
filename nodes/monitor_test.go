package nodes

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/log"
)

type testNode struct {
	id    string
	chain []*blockInfo
	head  int // points to where we're currently at, in the chain
}

type brokenNode struct {
	id string
}

func (b *brokenNode) Version() (string, error) {
	return "", errors.New("broken node")
}

func (b *brokenNode) Name() string {
	return b.id
}

func (b *brokenNode) Status() int {
	return NodeStatusUnreachable
}

func (b brokenNode) SetStatus(int) {}

func (b brokenNode) UpdateLatest() error {
	return errors.New("broken node")
}

func (b brokenNode) BlockAt(num uint64, force bool) *blockInfo {
	return nil
}

func (b brokenNode) HashAt(num uint64, force bool) common.Hash {
	return common.Hash{}
}

func (b brokenNode) HeadNum() uint64 {
	return 0
}

func (b brokenNode) LastProgress() int64 {
	return 0
}

func (b brokenNode) BadBlocks() []*eth.BadBlockArgs {
	return []*eth.BadBlockArgs{}
}

func newTestNode(id string, head int, chain []*blockInfo) *testNode {
	return &testNode{
		id,
		chain,
		head,
	}
}

func (t *testNode) Status() int {
	return 1
}

func (t *testNode) SetStatus(int) {}

func (t *testNode) Version() (string, error) {

	return "TestNode/v0.1/darwin/go1.4.1", nil
}

func (t *testNode) Name() string {
	return fmt.Sprintf("TestNode(%v)", t.id)
}

func (t *testNode) UpdateLatest() error {
	return nil
}

func (t *testNode) BlockAt(num uint64, force bool) *blockInfo {
	if num > uint64(t.head) {
		return nil
	}
	log.Trace("BlockAt", "node", t.id, "query", num)
	return t.chain[num]
}

func (t *testNode) HashAt(num uint64, force bool) common.Hash {
	if bl := t.BlockAt(num, force); bl != nil {
		return bl.hash
	}
	return common.Hash{}
}

func (t *testNode) HeadNum() uint64 {
	return uint64(t.head)
}

func (t *testNode) LastProgress() int64 {
	return 0
}

func (t *testNode) BadBlocks() []*eth.BadBlockArgs {
	return []*eth.BadBlockArgs{}
}

func TestMonitor(t *testing.T) {
	log.Root().SetHandler(log.LvlFilterHandler(
		log.LvlInfo, log.StreamHandler(os.Stderr, log.TerminalFormat(false))))

	//generate a base chain
	var a = make([]*blockInfo, 3000)
	var b = make([]*blockInfo, 3000)
	var c = make([]*blockInfo, 3000)

	for i := 0; i < len(a); i++ {
		h := common.BytesToHash(crypto.Keccak256([]byte(fmt.Sprintf("a :%d", i))))
		bl := &blockInfo{
			num:  uint64(i),
			hash: h,
		}
		a[i] = bl
	}
	// Another chain splits of at block 1000
	copy(b, a)
	for i := 1000; i < len(b); i++ {
		h := common.BytesToHash(crypto.Keccak256([]byte(fmt.Sprintf("b :%d", i))))
		bl := &blockInfo{
			num:  uint64(i),
			hash: h,
		}
		b[i] = bl
	}

	// Third chain splits from B at block 1500
	copy(c, b)
	for i := 1500; i < len(c); i++ {
		h := common.BytesToHash(crypto.Keccak256([]byte(fmt.Sprintf("c :%d", i))))
		bl := &blockInfo{
			num:  uint64(i),
			hash: h,
		}
		c[i] = bl
	}

	// spin up three nodes
	var nodes []Node
	nodes = append(nodes, newTestNode("node-a", 2704, a))
	nodes = append(nodes, newTestNode("node-b", 2800, b))
	nodes = append(nodes, newTestNode("node-c", 2202, c))
	// D is same as A, but two blocks behind
	nodes = append(nodes, newTestNode("node-d", 2702, a))

	nodes = append(nodes, &brokenNode{"broken-a"})
	nodes = append(nodes, &brokenNode{"broken-b"})
	nodes = append(nodes, &brokenNode{"broken-c"})

	mon, _ := NewMonitor(nodes, nil, time.Second)
	mon.doChecks()
}
