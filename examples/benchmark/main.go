package main

import (
	"benchmark/random"
	"benchmark/test"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"gitlab.com/Blockdaemon/go-tsm-sdkv2/v70/tsm"
	"golang.org/x/exp/rand"
	"golang.org/x/sync/errgroup"
)

func main() {
	b := NewBenchmark(os.Args[0])
	err := b.Run()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type Benchmark struct {

	// General parameters
	tsmConfigs     map[int]*tsm.Configuration
	operation      string
	ecdsaClients   int
	ed25519Clients int
	threshold      int
	signers        int
	duration       time.Duration
	showProgress   bool
	delay          time.Duration

	// Parameters used only for operation presigGen
	presigCount     int
	presigBatchSize uint64
	presigDir       string

	// Populated during benchmark
	clients           map[int]*tsm.Client
	ecdsaKeyID        string
	ed25519KeyID      string
	ecdsaOperations   uint64
	ed25519Operations uint64
}

func NewBenchmark(args string) Benchmark {
	b := Benchmark{}

	flagSet := flag.NewFlagSet(args, flag.ExitOnError)
	flagSet.StringVar(&b.operation, "operation", "sign", "Operation to perform; one of: sign, presigGen, onlineSign, getpub")
	flagSet.IntVar(&b.ecdsaClients, "ecdsaClients", 0, "Number of concurrent clients doing ECDSA signature requests")
	flagSet.IntVar(&b.ed25519Clients, "ed25519Clients", 0, "Number of concurrent clients doing Ed25519 signature requests")
	flagSet.IntVar(&b.threshold, "threshold", 0, "Security threshold. Default is number of MPC nodes - 1")
	flagSet.IntVar(&b.signers, "signers", 0, "Number of nodes to participate in signing. Default is threshold + 1. A random set of this size is chosen for each signature.")
	flagSet.DurationVar(&b.duration, "duration", 30*time.Second, "For how long should the test run")
	flagSet.BoolVar(&b.showProgress, "showProgress", false, "Print a line for each generated signature")
	flagSet.DurationVar(&b.delay, "delay", 0, "Duration that each client will sleep between each signature")

	flagSet.IntVar(&b.presigCount, "presigCount", 100, "Total number of presignatures each client will generate, if possible within test duration")
	flagSet.Uint64Var(&b.presigBatchSize, "presigBatchSize", 5, "Presiganture batch size")
	flagSet.StringVar(&b.presigDir, "presigDir", "./presigs", "Directory for storing presig IDs")

	var nodeURLs urlArray
	flagSet.Var(&nodeURLs, "node", "Specify an MPC node. Example: http://apikey@localhost:8080")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		flagSet.Usage()
		os.Exit(1)
	}

	if len(nodeURLs) == 0 {
		flagSet.Usage()
		os.Exit(1)
	}

	b.tsmConfigs = map[int]*tsm.Configuration{}
	for i, s := range nodeURLs {
		scheme, host, port, path, user, err := parseURL(s)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error parsing URL for MPC node %d: %s\n", i, err)
			flagSet.Usage()
			os.Exit(1)
		}
		tsmConfig := &tsm.Configuration{URL: fmt.Sprintf("%s://%s:%s%s", scheme, host, port, path)}
		if user != "" {
			tsmConfig = tsmConfig.WithAPIKeyAuthentication(user)
		}
		b.tsmConfigs[i] = tsmConfig
	}

	playerCount := len(b.tsmConfigs)

	if playerCount < 2 {
		_, _ = fmt.Fprintf(os.Stderr, "not enough players: %d\n", playerCount)
		flagSet.Usage()
		os.Exit(1)
	}

	if b.threshold == 0 {
		b.threshold = playerCount - 1
	}
	if b.threshold < 1 || b.threshold >= playerCount {
		_, _ = fmt.Fprintln(os.Stderr, "invalid threshold:", b.threshold)
		flagSet.Usage()
		os.Exit(1)
	}

	if b.signers == 0 {
		b.signers = b.threshold + 1
	}
	if b.signers < b.threshold+1 || b.signers > playerCount {
		_, _ = fmt.Fprintln(os.Stderr, "invalid signers:", b.signers)
		flagSet.Usage()
		os.Exit(1)
	}

	if b.ecdsaClients == 0 && b.ed25519Clients == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "at least one client required")
		flagSet.Usage()
		os.Exit(1)
	}

	return b
}

func (b *Benchmark) Run() error {
	fmt.Println("Running benchmark with the following parameters")
	fmt.Println()
	fmt.Println("Operation:       ", b.operation)
	fmt.Println("MPC nodes:       ", len(b.tsmConfigs))
	fmt.Println("ECDSA clients:   ", b.ecdsaClients)
	fmt.Println("Ed25519 clients: ", b.ed25519Clients)
	fmt.Println("Threshold:       ", b.threshold)
	fmt.Println("Signers:         ", b.signers)
	fmt.Println("Random delay:    ", b.delay)
	fmt.Println("Test duration:   ", b.duration)
	if b.operation == "presigGen" {
		fmt.Println("PresigCount:     ", b.presigCount)
		fmt.Println("PresigBatchSize: ", b.presigBatchSize)
	}
	fmt.Println()

	var err error
	b.clients, err = test.CreateClients(b.tsmConfigs)
	if err != nil {
		return err
	}

	startTime := time.Now()

	switch b.operation {
	case "sign":
		err = b.benchmarkSign()
	case "presigGen":
		err = b.benchmarkPresig()
	case "onlineSign":
		err = b.benchmarkOnline()
	case "getpub":
		err = b.benchmarkGetPub()
	default:
		err = fmt.Errorf("invalid operation: %s", b.operation)
	}
	if err != nil {
		return fmt.Errorf("benchmark failed: %w", err)
	}

	e2eDuration := time.Now().Sub(startTime)

	if b.ecdsaClients > 0 {
		opsPerSecond := float64(b.ecdsaOperations) / b.duration.Seconds()
		e2eOpsPerSecond := float64(b.ecdsaOperations) / e2eDuration.Seconds()

		fmt.Printf("ECDSA operations with %d clients: %d (%.2f ops/sec ; %0f ops/sec [e2e])\n", b.ecdsaClients, b.ecdsaOperations, opsPerSecond, e2eOpsPerSecond)
		if b.operation == "presigGen" {
			presigsPerSecond := float64(b.ecdsaOperations) * float64(b.presigBatchSize) / b.duration.Seconds()
			fmt.Printf(" - %.2f presigs/s\n", presigsPerSecond)
			e2ePresigsPerSecond := float64(b.ecdsaOperations) * float64(b.presigBatchSize) / e2eDuration.Seconds()
			fmt.Printf(" - %.2f presigs/s [e2e]\n", e2ePresigsPerSecond)
		}

	}
	if b.ed25519Clients > 0 {
		opsPerSecond := float64(b.ed25519Operations) / b.duration.Seconds()
		e2eOpsPerSecond := float64(b.ed25519Operations) / e2eDuration.Seconds()

		fmt.Printf("Ed25519 operations with %d clients: %d (%.2f ops/sec ; %.2f ops/sec [e2e])\n", b.ed25519Clients, b.ed25519Operations, opsPerSecond, e2eOpsPerSecond)
		if b.operation == "presigGen" {
			presigsPerSecond := float64(b.ed25519Operations) * float64(b.presigBatchSize) / b.duration.Seconds()
			fmt.Printf(" - %.2f presigs/s\n", presigsPerSecond)
			e2ePresigsPerSecond := float64(b.ed25519Operations) * float64(b.presigBatchSize) / e2eDuration.Seconds()
			fmt.Printf(" - %.2f presigs/s [e2e]\n", e2ePresigsPerSecond)
		}

	}

	return nil
}

func (b *Benchmark) benchmarkSign() error {
	err := b.generateKeys()
	if err != nil {
		return err
	}

	message := "This is the message that will be signed!"
	h := sha256.New()
	_, _ = h.Write([]byte(message))
	messageHash := h.Sum(nil)

	endTime := time.Now().Add(b.duration)
	var eg errgroup.Group
	for i := 0; i < b.ecdsaClients; i++ {
		i := i
		eg.Go(func() error {
			derivationPath := []uint32{1, 2, 3, 4, 5}
			for {

				if time.Now().After(endTime) {
					if b.showProgress {
						fmt.Println("ECDSA signer", i, "stopped")
					}
					break
				}

				derivationPath[4] += 1

				// Sign using a random subset of signers
				sessionConfig, selectedClients := subset(b.clients, b.signers)
				ecdsaSignFunc := func(playerIndex int, client *tsm.Client) error {
					_, err := client.ECDSA().Sign(context.TODO(), sessionConfig, b.ecdsaKeyID, derivationPath, messageHash)
					return err
				}
				if b.showProgress {
					players := make([]int, 0)
					for selected := range selectedClients {
						players = append(players, selected)
					}
					sort.Ints(players)
					fmt.Println("ECDSA signer", i, "signing with players", players)
				}
				err := test.RunClients(selectedClients, ecdsaSignFunc)
				if err != nil {
					fmt.Println("ECDSA signer", i, "error:", err)
					continue
				}

				signatureCount := atomic.AddUint64(&b.ecdsaOperations, 1)
				if b.showProgress {
					fmt.Println("ECDSA signatures:", signatureCount)
				}

				if b.delay > 0 {
					time.Sleep(time.Duration(rand.Int63n(int64(b.delay))) % b.delay)
				}

			}
			return nil
		})
	}

	for i := 0; i < b.ed25519Clients; i++ {
		i := i
		eg.Go(func() error {
			derivationPath := []uint32{1, 2, 3, 4, 5}
			for {

				if time.Now().After(endTime) {
					if b.showProgress {
						fmt.Println("Ed25519 signer", i, "stopped")
					}
					break
				}

				derivationPath[4] += 1
				sessionConfig, selectedClients := subset(b.clients, b.signers)
				ed25519SignFunc := func(playerIndex int, client *tsm.Client) error {
					_, err := client.Schnorr().Sign(context.TODO(), sessionConfig, b.ed25519KeyID, derivationPath, []byte(message))
					return err
				}

				err := test.RunClients(selectedClients, ed25519SignFunc)
				if err != nil {
					fmt.Println("Ed25519 signer", i, "error:", err)
					continue
				}

				signatureCount := atomic.AddUint64(&b.ed25519Operations, 1)
				if b.showProgress {
					fmt.Println("Ed25519 signatures:", signatureCount)
				}

				if b.delay > 0 {
					time.Sleep(time.Duration(rand.Int63n(int64(b.delay))) % b.delay)
				}

			}
			return nil
		})
	}

	return eg.Wait()

}

func (b *Benchmark) benchmarkPresig() error {
	err := b.generateKeys()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(b.presigDir, os.ModePerm); err != nil {
		return err
	}

	var eg errgroup.Group
	endTime := time.Now().Add(b.duration)

	for i := 0; i < b.ecdsaClients; i++ {
		i := i
		eg.Go(func() error {
			allECDSAPresigIDs := make([]string, 0)

			for {

				if time.Now().After(endTime) || len(allECDSAPresigIDs) >= b.presigCount {
					if b.showProgress {
						fmt.Println("ECDSA client", i, "stopped")
					}
					break
				}

				sessionID := tsm.GenerateSessionID()
				sessionConfig := tsm.NewStaticSessionConfig(sessionID, len(b.clients))
				ecdsaPresigFunc := func(playerIndex int, client *tsm.Client) error {
					presigIDs, err := client.ECDSA().GeneratePresignatures(context.TODO(), sessionConfig, b.ecdsaKeyID, b.presigBatchSize)
					if err != nil {
						return err
					}
					if playerIndex == 0 {
						allECDSAPresigIDs = append(allECDSAPresigIDs, presigIDs...)
					}
					return nil
				}

				err := test.RunClients(b.clients, ecdsaPresigFunc)
				if err != nil {
					fmt.Println("ECDSA client", i, "error:", err)
					continue
				}

				opCount := atomic.AddUint64(&b.ecdsaOperations, 1)
				if b.showProgress {
					percentage := (float64(len(allECDSAPresigIDs)) / float64(b.presigCount)) * 100.0
					fmt.Printf("ECDSA operations: %05d; client %04d generated presigs: %05d - %02.2f%%\n", opCount, i, len(allECDSAPresigIDs), percentage)
				}

				if b.delay > 0 {
					time.Sleep(time.Duration(rand.Int63n(int64(b.delay))) % b.delay)
				}

			}

			out := ECDSAPresigIDs{
				ECDSAKeyID: b.ecdsaKeyID,
				PresigIDs:  allECDSAPresigIDs,
			}
			outBytes, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			filePath := filepath.Join(b.presigDir, fmt.Sprintf("presig-ecdsa-client%04d.txt", i))
			err = os.WriteFile(filePath, outBytes, 0644)
			if err != nil {
				return err
			}
			fmt.Printf("ECDSA client %04d done, writing %d presig IDs to file %s\n", i, len(allECDSAPresigIDs), filePath)

			return nil
		})
	}

	for i := 0; i < b.ed25519Clients; i++ {
		i := i
		eg.Go(func() error {

			allEd25519PresigIDs := make([]string, 0)
			for {

				if time.Now().After(endTime) || len(allEd25519PresigIDs) >= b.presigCount {
					if b.showProgress {
						fmt.Println("Ed25519 client", i, "stopped")
					}
					break
				}

				sessionConfig := tsm.NewStaticSessionConfig(tsm.GenerateSessionID(), len(b.clients))
				ed25519PresigFunc := func(playerIndex int, client *tsm.Client) error {
					presigIDs, err := client.Schnorr().GeneratePresignatures(context.TODO(), sessionConfig, b.ed25519KeyID, b.presigBatchSize)
					if err != nil {
						return err
					}
					if playerIndex == 0 {
						allEd25519PresigIDs = append(allEd25519PresigIDs, presigIDs...)
					}
					return nil
				}

				err := test.RunClients(b.clients, ed25519PresigFunc)
				if err != nil {
					fmt.Println("Ed25519 client", i, "error:", err)
					continue
				}

				opCount := atomic.AddUint64(&b.ed25519Operations, 1)
				if b.showProgress {
					percentage := (float64(len(allEd25519PresigIDs)) / float64(b.presigCount)) * 100.0
					fmt.Printf("Ed25519 operations: %05d; client %04d generated presigs: %05d - %02.2f%%\n", opCount, i, len(allEd25519PresigIDs), percentage)
				}

				if b.delay > 0 {
					time.Sleep(time.Duration(rand.Int63n(int64(b.delay))) % b.delay)
				}

			}

			out := Ed25519PresigIDs{
				Ed25519KeyID: b.ed25519KeyID,
				PresigIDs:    allEd25519PresigIDs,
			}
			outBytes, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			filePath := filepath.Join(b.presigDir, fmt.Sprintf("presig-ed25519-client%04d.txt", i))
			err = os.WriteFile(filePath, outBytes, 0644)
			if err != nil {
				return err
			}
			fmt.Printf("Ed25519 client %04d done, writing %d presig IDs to file %s\n", i, len(allEd25519PresigIDs), filePath)
			return nil
		})
	}

	return eg.Wait()

}

func (b *Benchmark) benchmarkOnline() error {
	var eg errgroup.Group
	endTime := time.Now().Add(b.duration)

	for i := 0; i < b.ecdsaClients; i++ {
		i := i
		eg.Go(func() error {

			message := "This is the message that will be signed!"
			h := sha256.New()
			_, _ = h.Write([]byte(message))
			messageHash := h.Sum(nil)

			// Read key ID and presig IDs

			presigFilePath := filepath.Join(b.presigDir, fmt.Sprintf("presig-ecdsa-client%04d.txt", i))
			presigFile, err := os.Open(presigFilePath)
			if err != nil {
				return fmt.Errorf("failed to read presigs from %s", presigFile.Name())
			}
			defer func() { _ = presigFile.Close() }()
			jsonBytes, _ := io.ReadAll(presigFile)
			var presigs ECDSAPresigIDs
			err = json.Unmarshal(jsonBytes, &presigs)
			if err != nil {
				return err
			}

			fmt.Println("ECDSA client", i, "read", len(presigs.PresigIDs), "presig IDs from", presigFilePath)

			derivationPath := []uint32{1, 2, 3, 4, 5}
			for {

				if time.Now().After(endTime) || len(presigs.PresigIDs) == 0 {
					if b.showProgress {
						fmt.Println("ECDSA client", i, "stopped")
					}
					break
				}

				// Do online signing using next presignature ID
				derivationPath[4]++
				var presigID string
				presigID, presigs.PresigIDs = presigs.PresigIDs[0], presigs.PresigIDs[1:]
				ecdsaSignWithPresigFunc := func(playerIndex int, client *tsm.Client) error {
					_, err := client.ECDSA().SignWithPresignature(context.TODO(), presigs.ECDSAKeyID, presigID, derivationPath, messageHash)
					if err != nil {
						return err
					}
					return nil
				}

				err := test.RunClients(b.clients, ecdsaSignWithPresigFunc)
				if err != nil {
					fmt.Println("ECDSA client", i, "error:", err)
					continue
				}

				opCount := atomic.AddUint64(&b.ecdsaOperations, 1)
				if b.showProgress {
					fmt.Printf("ECDSA operations: %05d; client %04d presigs left: %05d\n", opCount, i, len(presigs.PresigIDs))
				}

				if b.delay > 0 {
					time.Sleep(time.Duration(rand.Int63n(int64(b.delay))) % b.delay)
				}

			}

			return nil
		})
	}

	for i := 0; i < b.ed25519Clients; i++ {
		i := i
		eg.Go(func() error {

			message := "This is the message that will be signed!"

			// Read key ID and presig IDs

			presigFilePath := filepath.Join(b.presigDir, fmt.Sprintf("presig-ed25519-client%04d.txt", i))
			presigFile, err := os.Open(presigFilePath)
			if err != nil {
				return fmt.Errorf("failed to read presigs from %s", presigFile.Name())
			}
			defer func() { _ = presigFile.Close() }()
			jsonBytes, _ := io.ReadAll(presigFile)
			var presigs Ed25519PresigIDs
			err = json.Unmarshal([]byte(jsonBytes), &presigs)
			if err != nil {
				return err
			}
			fmt.Println("Ed25519 client", i, "read", len(presigs.PresigIDs), "presig IDs from", presigFilePath)

			derivationPath := []uint32{1, 2, 3, 4, 5}
			for {

				if time.Now().After(endTime) || len(presigs.PresigIDs) == 0 {
					if b.showProgress {
						fmt.Println("Ed25519 client", i, "stopped")
					}
					break
				}

				// Do online signing using next presignature ID

				derivationPath[4]++
				var presigID string
				presigID, presigs.PresigIDs = presigs.PresigIDs[0], presigs.PresigIDs[1:]
				ed25519SignWithPresigFunc := func(playerIndex int, client *tsm.Client) error {
					_, err := client.Schnorr().SignWithPresignature(context.TODO(), presigs.Ed25519KeyID, presigID, derivationPath, []byte(message))
					if err != nil {
						return err
					}
					return nil
				}

				err := test.RunClients(b.clients, ed25519SignWithPresigFunc)
				if err != nil {
					fmt.Println("Ed25519 client", i, "error:", err)
					continue
				}

				opCount := atomic.AddUint64(&b.ed25519Operations, 1)
				if b.showProgress {
					fmt.Printf("Ed25519 operations: %05d; client %04d presigs left: %05d\n", opCount, i, len(presigs.PresigIDs))
				}

				if b.delay > 0 {
					time.Sleep(time.Duration(rand.Int63n(int64(b.delay))) % b.delay)
				}

			}

			return nil
		})
	}

	return eg.Wait()

}

func (b *Benchmark) benchmarkGetPub() error {
	err := b.generateKeys()
	if err != nil {
		return err
	}

	endTime := time.Now().Add(b.duration)
	var eg errgroup.Group

	for i := 0; i < b.ecdsaClients; i++ {
		i := i
		eg.Go(func() error {
			derivationPath := []uint32{1, 2, 3, 4, 5}
			for {

				if time.Now().After(endTime) {
					if b.showProgress {
						fmt.Println("ECDSA client", i, "stopped")
					}
					break
				}

				derivationPath[4]++
				getPubFunc := func(playerIndex int, client *tsm.Client) error {
					_, err := client.ECDSA().PublicKey(context.TODO(), b.ecdsaKeyID, derivationPath)
					if err != nil {
						return err
					}
					return nil
				}

				err := test.RunClients(b.clients, getPubFunc)
				if err != nil {
					fmt.Println("ECDSA client", i, "error:", err)
					continue
				}

				opCount := atomic.AddUint64(&b.ecdsaOperations, 1)
				if b.showProgress {
					fmt.Printf("ECDSA operations: %05d", opCount)
				}

				if b.delay > 0 {
					time.Sleep(time.Duration(rand.Int63n(int64(b.delay))) % b.delay)
				}

			}

			return nil
		})
	}

	for i := 0; i < b.ed25519Clients; i++ {
		i := i
		eg.Go(func() error {
			derivationPath := []uint32{1, 2, 3, 4, 5}
			for {

				if time.Now().After(endTime) {
					if b.showProgress {
						fmt.Println("Ed25519 client", i, "stopped")
					}
					break
				}

				derivationPath[4]++
				getPubFunc := func(playerIndex int, client *tsm.Client) error {
					_, err := client.Schnorr().PublicKey(context.TODO(), b.ed25519KeyID, []uint32{})
					if err != nil {
						return err
					}
					return nil
				}

				err := test.RunClients(b.clients, getPubFunc)
				if err != nil {
					fmt.Println("Ed25519 client", i, "error:", err)
					continue
				}

				opCount := atomic.AddUint64(&b.ed25519Operations, 1)
				if b.showProgress {
					fmt.Printf("Ed25519 operations: %05d", opCount)
				}

				if b.delay > 0 {
					time.Sleep(time.Duration(rand.Uint64()) % b.delay)
				}

			}

			return nil
		})
	}

	return eg.Wait()

}

func (b *Benchmark) generateKeys() error {

	b.ecdsaKeyID = random.String(20)
	if b.ecdsaClients > 0 {
		sessionConfig := test.CreateSessionConfig(b.clients)
		ecdsaKeyGenFunc := func(playerIndex int, client *tsm.Client) error {
			_, err := client.ECDSA().GenerateKey(context.TODO(), sessionConfig, b.threshold, "secp256k1", b.ecdsaKeyID)
			return err
		}
		err := test.RunClients(b.clients, ecdsaKeyGenFunc)
		if err != nil {
			return fmt.Errorf("error running keygen for ECDSA: %w", err)
		}
	}

	b.ed25519KeyID = random.String(20)
	if b.ed25519Clients > 0 {
		sessionConfig := test.CreateSessionConfig(b.clients)
		ed25519KeyGenFunc := func(playerIndex int, client *tsm.Client) error {
			_, err := client.Schnorr().GenerateKey(context.TODO(), sessionConfig, b.threshold, "ED-25519", b.ed25519KeyID)
			return err
		}
		err := test.RunClients(b.clients, ed25519KeyGenFunc)
		if err != nil {
			return fmt.Errorf("error running keygen for Ed25519: %w", err)
		}
	}

	return nil
}

type ECDSAPresigIDs struct {
	ECDSAKeyID string
	PresigIDs  []string
}

type Ed25519PresigIDs struct {
	Ed25519KeyID string
	PresigIDs    []string
}

type urlArray []*url.URL

func (s *urlArray) String() string {
	var x []string
	for i := range *s {
		x = append(x, (*s)[i].String())
	}
	return strings.Join(x, " ")
}

func (s *urlArray) Set(v string) error {
	u, err := url.Parse(v)
	if err != nil {
		return err
	}
	*s = append(*s, u)
	return nil
}

// Returns a random subset of clients, along with a session configuration for these clients
func subset(clients map[int]*tsm.Client, size int) (*tsm.SessionConfig, map[int]*tsm.Client) {
	clientsSubset := make(map[int]*tsm.Client, size)

	i := 0
	players := make([]int, len(clients))
	for p := range clients {
		players[i] = p
		i++
	}

	rand.Shuffle(len(players), func(i, j int) { players[i], players[j] = players[j], players[i] })
	selected := players[:size]
	for _, p := range selected {
		clientsSubset[p] = clients[p]
	}

	sort.Ints(selected)
	sessionConfig := tsm.NewSessionConfig(tsm.GenerateSessionID(), selected, nil)
	return sessionConfig, clientsSubset
}

func parseURL(u *url.URL) (scheme, host, port, path, user string, err error) {
	scheme = strings.ToLower(u.Scheme)
	if scheme == "" {
		return "", "", "", "", "", fmt.Errorf("missing scheme")
	}
	if scheme != "http" && scheme != "https" {
		return "", "", "", "", "", fmt.Errorf("invalid scheme")
	}

	host = u.Hostname()
	if host == "" {
		return "", "", "", "", "", fmt.Errorf("missing host")
	}

	port = u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	path = u.EscapedPath()
	if path == "" {
		path = "/"
	}

	if u.User != nil {
		user = u.User.Username()
	}

	return scheme, host, port, path, user, nil
}
