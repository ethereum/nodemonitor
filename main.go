package main

import (
	"errors"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/holiman/nodemonitor/nodes"
	"github.com/naoina/toml"
)

// ssh -L 8546:localhost:8545 ubuntu@nethermind.ethdevops.io
// ssh -L 8547:localhost:8545 ubuntu@besu.ethdevops.io
// ssh -L 8548:localhost:8545 ubuntu@mon02.ethdevops.io
func main() {
	// Initialize the logger
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlInfo, log.StreamHandler(os.Stderr, log.TerminalFormat(false))))

	if len(os.Args) < 2 {
		log.Error("Second arg must be path to config file")
		os.Exit(1)
	}
	cFile := os.Args[1]
	f, err := os.Open(cFile)
	if err != nil {
		log.Error("Error", "error", err)
		os.Exit(1)
	}
	defer f.Close()

	var config nodes.Config
	if err := toml.NewDecoder(f).Decode(&config); err != nil {
		log.Error("Error", "error", err)
		os.Exit(1)
	}
	nodes.EnableMetrics(&config)

	mon, err := spinupMonitor(config)
	if err != nil {
		log.Error("Error", "error", err)
		os.Exit(1)
	}

	spinupServer(config)

	mon.Start()
	// Wait for ctrl-c
	quitCh := make(chan os.Signal, 1)
	signal.Notify(quitCh, os.Interrupt)

	// TODO: Monitor changes to the config file

	<-quitCh
	mon.Stop()
	os.Exit(0)
}

func spinupMonitor(config nodes.Config) (*nodes.NodeMonitor, error) {
	db, err := nodes.NewBlockDB()
	if err != nil {
		return nil, err
	}
	reload, err := time.ParseDuration(config.ReloadInterval)
	if err != nil {
		return nil, err
	}
	var clients []nodes.Node
	for _, c := range config.Clients {
		var (
			node nodes.Node
			err  error
		)
		switch c.Kind {
		case "infura":
			node, err = nodes.NewInfuraNode(c.Name, config.InfuraKey, config.InfuraEndpoint,
				db, c.Ratelimit)
		case "alchemy":
			node, err = nodes.NewAlchemyNode(c.Name, config.AlchemyKey, config.AlchemyEndpoint,
				db, c.Ratelimit)
		case "rpc":
			node, err = nodes.NewRPCNode(c.Name, c.Url, db, c.Ratelimit)
		case "etherscan":
			node, err = nodes.NewEtherscanNode(c.Name, config.EtherscanKey, config.EtherscanEndpoint,
				db, c.Ratelimit)
		case "testnode-canon":
			node = nodes.NewLiveTestNode("canon", 13_000_000, []uint64{0}, []int{0})
		case "testnode-fork-old":
			node = nodes.NewLiveTestNode("old", 12_800_000, []uint64{0, 12_799_998}, []int{0, 2})
		case "testnode-fork-recent":
			node = nodes.NewLiveTestNode("legacy", 12_999_900, []uint64{0, 12_999_800}, []int{0, 1})
		default:
			log.Error("Wrong client type", "kind", c.Kind, "available", "[rpc, infura, alchemy]")
			return nil, errors.New("invalid config")
		}
		if err != nil {
			return nil, err
		}
		clients = append(clients, node)
		log.Info("Client configured", "name", c.Name)
	}

	return nodes.NewMonitor(clients, db, reload)
}

func spinupServer(config nodes.Config) error {
	if len(config.ServerAddress) == 0 {
		return nil
	}
	fs := http.FileServer(http.Dir("www/"))
	http.Handle("/", http.StripPrefix("/", fs))
	log.Info("Starting web server", "address", config.ServerAddress)
	go http.ListenAndServe(config.ServerAddress, nil)
	return nil
}
