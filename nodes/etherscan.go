package nodes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/metrics"
	"go.uber.org/ratelimit"
)

// etherscanMethodCaller wraps calls to etherscan.
type etherscanMethodCaller struct {
	url    string
	apiKey string
}

func NewEtherscanHeaderCall(url, apiKey string) *etherscanMethodCaller {
	return &etherscanMethodCaller{url: url, apiKey: apiKey}
}

func (caller *etherscanMethodCaller) Version() (string, error) {
	return "Not available", nil
}

func (caller *etherscanMethodCaller) GetBadBlocks() ([]*eth.BadBlockArgs, error) {
	return []*eth.BadBlockArgs{}, nil
}

type jsonrpcMessage struct {
	Version string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (caller *etherscanMethodCaller) HeaderByNumber(num *big.Int) (*types.Header, error) {
	action := "eth_getBlockByNumber"
	tag := num.String()
	if num == nil {
		tag = "latest"
	}
	// https://api.etherscan.io/api?module=proxy&action=eth_getBlockByNumber&tag=0x10d4f&boolean=true&apikey=YourApiKeyToken
	url := fmt.Sprintf("%s?module=proxy&action=%s&tag=%s&boolean=true&apikey=%s", caller.url, action, tag, caller.apiKey)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	var res jsonrpcMessage
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	var head *types.Header
	if err := json.Unmarshal(res.Result, &head); err != nil {
		return nil, err
	}
	return head, nil
}

func NewEtherscanNode(name, apiKey, endpoint string, db *blockDB, rateLimit int) (*RemoteNode, error) {
	if len(apiKey) == 0 {
		return nil, errors.New("Missing etherscan_key")
	}
	gaugeName := fmt.Sprintf("head/%v", name)
	throttle := ratelimit.NewUnlimited()
	if rateLimit > 0 {
		throttle = ratelimit.New(rateLimit)
	}

	return &RemoteNode{
		RPCMethodCaller: NewEtherscanHeaderCall(endpoint, apiKey),
		name:            name,
		version:         "Etherscan",
		chainHistory:    make(map[uint64]*blockInfo),
		db:              db,
		headGauge:       metrics.GetOrRegisterGauge(gaugeName, registry),
		throttle:        throttle,
		lastCheck:       make(map[string]time.Time),
	}, nil
}
