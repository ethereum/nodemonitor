package nodes

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"os"
	"testing"
)

func TestInfura(t *testing.T) {
	key := os.Getenv("INFURA_KEY")
	fmt.Printf("key: %v\n", key)
	node, err := NewInfuraNode("Infura", key, "https://mainnet.infura.io/v3/", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.UpdateLatest(); err != nil {
		t.Fatal(err)
	}
	if node.HeadNum() == 0 {
		t.Errorf("Got latest block 0")
	}
	t.Logf("Latest is %v", node.HeadNum())
}

func TestAlchemy(t *testing.T) {
	key := os.Getenv("ALCHEMY_KEY")
	fmt.Printf("key: %v\n", key)
	node, err := NewAlchemyNode("Alchemy", key, "https://eth-mainnet.alchemyapi.io/v2/", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.UpdateLatest(); err != nil {
		t.Fatal(err)
	}
	if node.HeadNum() == 0 {
		t.Errorf("Got latest block 0")
	}
	t.Logf("Latest is %v", node.HeadNum())
}

func TestEtherscan(t *testing.T) {
	key := os.Getenv("ETHERSCAN_KEY")
	fmt.Printf("key: %v\n", key)
	node, err := NewEtherscanNode("Etherscan", key, "https://api.etherscan.io/api", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.UpdateLatest(); err != nil {
		t.Fatal(err)
	}
	if node.HeadNum() == 0 {
		t.Errorf("Got latest block 0")
	}
	t.Logf("Latest is %v", node.HeadNum())

	got10 := node.HashAt(10, false)
	want10 := common.HexToHash("0x4ff4a38b278ab49f7739d3a4ed4e12714386a9fdf72192f2e8f7da7822f10b4d")
	if got10 != want10 {
		want16 := common.HexToHash("0x9657beaf8542273d7448f6d277bb61aef0f700a91b238ac8b34c020f7fb8664c")
		if got10 == want16 {
			t.Errorf("error, base mismatch, requested block 10 but got block 16!")
		} else {
			t.Errorf("error: want %x, got %x", got10, want10)
		}
	}
}
