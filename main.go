package main

import (
	"os"
	"os/signal"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/holiman/nodemonitor/nodes"
	"github.com/naoina/toml"
)

// ssh -L 8546:localhost:8545 ubuntu@nethermind.ethdevops.io
// ssh -L 8547:localhost:8545 ubuntu@besu.ethdevops.io
// ssh -L 8548:localhost:8545 ubuntu@mon02.ethdevops.io
func main() {
	// Initialize the logger
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlInfo, log.StreamHandler(os.Stderr, log.TerminalFormat(false))))

	f, err := os.Open("config.toml")
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
	// then to use the unmarshaled config...
	for _, c := range config.Clients {
		if c.URL() == nil {
			log.Error("Error: client missing url", "name", c.Name)
			os.Exit(1)
		}
		log.Info("Client configured", "name", c.Name, "url", c.URL().String())
	}
	mon, err := spinupMonitor(config)
	if err != nil {
		log.Error("Error", "error", err)
		os.Exit(1)
	}
	mon.Start()
	// Wait for ctrl-c
	quitCh := make(chan os.Signal, 1)
	signal.Notify(quitCh, os.Interrupt)

	<-quitCh
	mon.Stop()
	os.Exit(0)
}

func spinupMonitor(config nodes.Config) (*nodes.NodeMonitor, error) {
	db, err := nodes.NewBlockDB()
	if err != nil {
		return nil, err
	}
	var clients []nodes.Node
	for _, cli := range config.Clients {
		rpcCli, err := rpc.Dial(cli.URL().String())
		if err != nil {
			return nil, err
		}
		clients = append(clients, nodes.NewRPCNode(rpcCli, db))
	}
	return nodes.NewMonitor(clients, db)
}
