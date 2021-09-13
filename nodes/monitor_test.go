package nodes

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/log"
)

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

func (b brokenNode) BadBlockCount() int {
	return 0
}

func TestMonitor(t *testing.T) {
	log.Root().SetHandler(log.LvlFilterHandler(
		log.LvlCrit, log.StreamHandler(os.Stderr, log.TerminalFormat(false))))

	// Disable the vuln check for tests
	disableVulnCheck = true

	// spin up three nodes
	var nodes []Node

	// 10 nodes are in agreement
	nodes = append(nodes, newTestNode("canon-a", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-b", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-c", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-d", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-e", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-f", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-g", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-h", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-i", 13_000_000, []uint64{0}, []int{0}))
	nodes = append(nodes, newTestNode("canon-j", 13_000_000, []uint64{0}, []int{0}))

	// Three nodes forked off 200 blocks earlier, and are 100 blocks behind too
	nodes = append(nodes, newTestNode("fork-a", 12_999_900, []uint64{0, 12_999_800}, []int{0, 1}))
	nodes = append(nodes, newTestNode("fork-b", 12_999_900, []uint64{0, 12_999_800}, []int{0, 1}))
	nodes = append(nodes, newTestNode("fork-c", 12_999_900, []uint64{0, 12_999_800}, []int{0, 1}))

	// And one got stuck on a hardfork, it progressed only two blocks
	nodes = append(nodes, newTestNode("old-a", 12_800_000, []uint64{0, 12_799_998}, []int{0, 2}))
	// Two nodes are br0ken
	nodes = append(nodes, &brokenNode{"broken-a"})
	nodes = append(nodes, &brokenNode{"broken-b"})

	nm, err := NewMonitor(nodes, nil, time.Second, "Playdoh-net")
	if err != nil {
		t.Fatal(err)
	}
	if nm.lastReport == nil {
		t.Fatalf("missing report")
	}
	// Check the 'interesting numbers'. We expect the following:
	// heads: 13M, 12_999_900, 12_800_000
	// forks: 12_999_800, 12_799_998
	// fork parents:12999799, 12_799_998
	if have, want := len(nm.lastReport.Numbers), 7; have != want {
		nm.lastReport.Print()
		t.Fatalf("wrong numbers, want %d, have %d", want, have)
	}
	var countQueries = func() int {
		queries := 0
		for _, node := range nodes {
			tn, ok := node.(*testNode)
			if ok {
				//t.Logf("%v was queried for %d blocks, totalling %d queries", tn.Name(),
				//	len(tn.queriedNumbers), tn.totalQueries)
				queries += len(tn.queriedNumbers)
			}
		}
		return queries
	}
	q1 := countQueries()
	t.Logf("Initial check: %d unique block queries", q1)
	// Now test the same again, without any progression of the nodess
	nm.doChecks()
	q2 := countQueries() - q1
	t.Logf("Follow-up check: %d unique block queries", q2)
	if q2 != 0 {
		t.Logf("expected zero queries on follow-up, got %d", q2)
	}
	// Progress all nodes 2 blocks
	for _, node := range nodes {
		tn, ok := node.(*testNode)
		if ok {
			tn.head += 2
		}
	}
	// Now test the same again, after block progression
	nm.doChecks()
	q3 := countQueries() - q1 - q2
	t.Logf("Follow-up check after block progression: %d unique block queries", q3)

	// Progress all nodes 2 blocks and fork
	for _, node := range nodes {
		tn, ok := node.(*testNode)
		if ok {
			tn.head += 2
		}
	}
	nodes[2].(*testNode).head += 2
	nodes[2].(*testNode).forks = append(nodes[2].(*testNode).forks, 13_000_004)
	nodes[2].(*testNode).seeds = append(nodes[2].(*testNode).seeds, 4)
	// Now test the same again, after block progression
	nm.doChecks()
	q4 := countQueries() - q1 - q2 - q3
	t.Logf("Follow-up check after block progression and fork: %d unique block queries", q4)
}
