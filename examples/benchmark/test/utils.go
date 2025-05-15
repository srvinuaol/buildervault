package test

import (
	"fmt"
	"sort"

	"gitlab.com/Blockdaemon/go-tsm-sdkv2/v70/tsm"
	"golang.org/x/sync/errgroup"
)

func CreateClients(configs map[int]*tsm.Configuration) (map[int]*tsm.Client, error) {
	clients := make(map[int]*tsm.Client)
	for i, c := range configs {
		var err error
		clients[i], err = tsm.NewClient(c)
		if err != nil {
			return nil, err
		}
	}
	return clients, nil
}

func CreateSessionConfig(clients map[int]*tsm.Client) *tsm.SessionConfig {
	var players []int
	for i := range clients {
		players = append(players, i)
	}
	sort.Ints(players)
	return tsm.NewSessionConfig(tsm.GenerateSessionID(), players, nil)
}

func RunClients(clients map[int]*tsm.Client, runFunc func(playerIndex int, client *tsm.Client) error) error {
	var eg errgroup.Group
	for i, c := range clients {
		i, c := i, c
		eg.Go(func() error {
			err := runFunc(i, c)
			if err != nil {
				return fmt.Errorf("client %d failed: %v", i, err)
			}
			return nil
		})
	}
	return eg.Wait()
}
