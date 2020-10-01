package nodes

import (
	"fmt"
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
}
