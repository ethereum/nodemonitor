package nodes

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/rpc"
	"go.uber.org/ratelimit"
)

type RPCMethodCaller interface {
	HeaderByNumber(*big.Int) (*types.Header, error)
	Version() (string, error)
}

type JSONRPCMethodCaller struct {
	rpcCli *rpc.Client
	ethCli *ethclient.Client
}

func NewRPCHeaderCall(rpcCli *rpc.Client, ethCli *ethclient.Client) *JSONRPCMethodCaller {
	return &JSONRPCMethodCaller{
		rpcCli: rpcCli,
		ethCli: ethCli,
	}
}

func (caller *JSONRPCMethodCaller) Version() (string, error) {
	method := "web3_clientVersion"
	var ver string
	err := caller.rpcCli.CallContext(context.Background(), &ver, method)
	return ver, err
}

func (caller *JSONRPCMethodCaller) HeaderByNumber(num *big.Int) (*types.Header, error) {
	return caller.ethCli.HeaderByNumber(context.Background(), num)
}

// RemoteNode represents a remote node that we can make queries against
type RemoteNode struct {
	RPCMethodCaller // The actual call implementation, json-rpc or http queries
	// Some local cached values
	version      string
	name         string
	latest       *blockInfo
	chainHistory map[uint64]*blockInfo
	// backend to store hash -> header into
	db     *blockDB
	status int

	lastProgress int64 // Last unix-time the node progressed the chain

	headGauge metrics.Gauge
	// rate limiting
	throttle  ratelimit.Limiter
	lastCheck map[string]time.Time
}

func NewRPCNode(name string, url string, db *blockDB, rateLimit int) (*RemoteNode, error) {
	rpcCli, err := rpc.Dial(url)
	if err != nil {
		return nil, err
	}
	throttle := ratelimit.NewUnlimited()
	if rateLimit > 0 {
		throttle = ratelimit.New(rateLimit)
	}
	ethCli := ethclient.NewClient(rpcCli)
	gaugeName := fmt.Sprintf("head/%v", name)
	return &RemoteNode{
		RPCMethodCaller: NewRPCHeaderCall(rpcCli, ethCli),
		name:            name,
		version:         "n/a",
		chainHistory:    make(map[uint64]*blockInfo),
		db:              db,
		headGauge:       metrics.GetOrRegisterGauge(gaugeName, registry),
		throttle:        throttle,
		lastCheck:       make(map[string]time.Time),
	}, nil
}

func NewInfuraNode(name, projectId, endpoint string, db *blockDB, rateLimit int) (*RemoteNode, error) {
	if len(projectId) == 0 {
		return nil, errors.New("Missing infura_key")
	}
	url := fmt.Sprintf("%v%v", endpoint, projectId)
	rpcCli, err := rpc.Dial(url)
	if err != nil {
		return nil, err
	}
	ethCli := ethclient.NewClient(rpcCli)
	gaugeName := fmt.Sprintf("head/%v", name)
	throttle := ratelimit.NewUnlimited()
	if rateLimit > 0 {
		throttle = ratelimit.New(rateLimit)
	}
	return &RemoteNode{
		RPCMethodCaller: NewRPCHeaderCall(rpcCli, ethCli),
		name:            name,
		version:         "Infura V3",
		chainHistory:    make(map[uint64]*blockInfo),
		db:              db,
		headGauge:       metrics.GetOrRegisterGauge(gaugeName, registry),
		throttle:        throttle,
		lastCheck:       make(map[string]time.Time),
	}, nil
}

func NewAlchemyNode(name, apiKey, endpoint string, db *blockDB, rateLimit int) (*RemoteNode, error) {
	if len(apiKey) == 0 {
		return nil, errors.New("Missing alchemy_key")
	}
	url := fmt.Sprintf("%v%v", endpoint, apiKey)
	rpcCli, err := rpc.Dial(url)
	if err != nil {
		return nil, err
	}
	ethCli := ethclient.NewClient(rpcCli)
	gaugeName := fmt.Sprintf("head/%v", name)
	throttle := ratelimit.NewUnlimited()
	if rateLimit > 0 {
		throttle = ratelimit.New(rateLimit)
	}
	return &RemoteNode{
		RPCMethodCaller: NewRPCHeaderCall(rpcCli, ethCli),
		name:            name,
		version:         "Alchemy V2",
		chainHistory:    make(map[uint64]*blockInfo),
		db:              db,
		headGauge:       metrics.GetOrRegisterGauge(gaugeName, registry),
		throttle:        throttle,
		lastCheck:       make(map[string]time.Time),
	}, nil
}

func (node *RemoteNode) SetStatus(status int) {
	node.status = status
}

func (node *RemoteNode) Status() int {
	return node.status
}

func (node *RemoteNode) Version() (string, error) {
	method := "web3_clientVersion"
	// Don't request version more than once every 30 seconds
	if time.Since(node.lastCheck[method]) < time.Second*30 {
		return node.version, nil
	}
	node.lastCheck[method] = time.Now()

	node.throttle.Take()
	ver, err := node.RPCMethodCaller.Version()
	if err == nil {
		node.version = ver
	}
	return ver, err
}

func (node *RemoteNode) HeadNum() uint64 {
	if node.latest != nil {
		return node.latest.num
	}
	return 0
}

func (node *RemoteNode) Name() string {
	return node.name
}

func (node *RemoteNode) LastProgress() int64 {
	return node.lastProgress
}

func (node *RemoteNode) UpdateLatest() error {
	bl, err := node.fetchHeader(nil)
	if err != nil {
		return err
	}
	if node.latest == nil || node.latest.hash != bl.hash {
		node.lastProgress = time.Now().Unix()
		node.latest = bl
		node.headGauge.Update(int64(bl.num))
		log.Info("Set last progress to ", "time", node.lastProgress)
	}
	return nil
}

// throttledGetHeader fetches header at num, applies throttling and
// stores the header info in the node chain and the backend
func (node *RemoteNode) throttledGetHeader(num *big.Int) (*blockInfo, error) {
	node.throttle.Take()
	log.Debug("Doing check", "node", node.name, "requested", num)
	h, err := node.RPCMethodCaller.HeaderByNumber(num)
	if err != nil {
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
		num:   h.Number.Uint64(),
		hash:  h.Hash(),
		pHash: h.ParentHash,
	}
	node.chainHistory[bl.num] = bl
	return bl, nil
}

func (node *RemoteNode) fetchHeader(num *big.Int) (*blockInfo, error) {
	hdr, err := node.throttledGetHeader(num)
	if err != nil {
		return hdr, err
	}
	// If we have a parent for this block, we can check if it's still valid
	if parentInfo, ok := node.chainHistory[hdr.num-1]; ok {
		current := hdr
		reorgs := 0
		for parentInfo != nil {
			if parentInfo.hash == current.pHash {
				break // not reorged
			}
			reorgs++
			delete(node.chainHistory, parentInfo.num) // wipe and refetch parent
			current, err = node.throttledGetHeader(new(big.Int).SetUint64(parentInfo.num))
			if err != nil {
				break
			}
			parentInfo = node.chainHistory[current.num-1]
		}
		if reorgs > 1 {
			log.Info("Node reorged", "name", node.name, "size", reorgs)
		}
	}

	return hdr, nil
}

func (node *RemoteNode) BlockAt(num uint64, force bool) *blockInfo {
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

func (node *RemoteNode) HashAt(num uint64, force bool) common.Hash {
	if bl := node.BlockAt(num, force); bl != nil {
		return bl.hash
	}
	return common.Hash{}
}
