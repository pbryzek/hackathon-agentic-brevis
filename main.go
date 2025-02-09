package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"sync"

	"github.com/brevis-network/brevis-sdk/sdk"
	"github.com/ethereum/go-ethereum/common"
)

type AppCircuit struct {
	EmissionsData *big.Int
}

var (
	circuitPrepared bool
	circuitMutex    sync.Mutex
)

var _ sdk.AppCircuit = &AppCircuit{}

func (c *AppCircuit) Allocate() (maxReceipts, maxStorage, maxTransactions int) {
	return 0, 32, 0
}

func (c *AppCircuit) Define(api *sdk.CircuitAPI, in sdk.DataInput) error {
	slots := sdk.NewDataStream(api, in.StorageSlots)
	expectedEmission := sdk.ConstUint248(c.EmissionsData.Uint64())

	sdk.AssertEach(slots, func(slot sdk.StorageSlot) sdk.Uint248 {
		emissionValue := api.ToUint248(slot.Value)
		return api.Uint248.IsEqual(emissionValue, expectedEmission)
	})

	emissions := sdk.Map(slots, func(slot sdk.StorageSlot) sdk.Uint248 {
		return api.ToUint248(slot.Value)
	})
	totalEmissions := sdk.Sum(emissions)

	api.OutputUint(248, totalEmissions)

	return nil
}

func handlePrepareDownload(w http.ResponseWriter, r *http.Request) {
	go func() {
		circuitMutex.Lock()
		defer circuitMutex.Unlock()

		if circuitPrepared {
			log.Println("Circuit already prepared.")
			return
		}

		rpcURL := "https://sepolia.drpc.org"
		outputDir := "./brevis-output"
		app, err := sdk.NewBrevisApp(11155111, rpcURL, outputDir)
		if err != nil {
			log.Printf("Error initializing BrevisApp: %v", err)
			return
		}

		estimatedEmissions := big.NewInt(10000)
		circuit := &AppCircuit{EmissionsData: estimatedEmissions}

		outDir := "./brevis-circuit"
		srsDir := "./brevis-srs"
		_, _, _, _, err = sdk.Compile(circuit, outDir, srsDir, app)
		if err != nil {
			log.Printf("Error compiling circuit: %v", err)
			return
		}

		circuitPrepared = true
		log.Println("Circuit preparation complete.")
	}()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Circuit preparation started."))
}

func handleSubmitProof(w http.ResponseWriter, r *http.Request) {
	circuitMutex.Lock()
	prepared := circuitPrepared
	circuitMutex.Unlock()

	if !prepared {
		http.Error(w, "Circuit not prepared yet. Please try again later.", http.StatusBadRequest)
		return
	}

	rpcURL := "https://sepolia.drpc.org"
	outputDir := "./brevis-output"
	app, err := sdk.NewBrevisApp(11155111, rpcURL, outputDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error initializing BrevisApp: %v", err), http.StatusInternalServerError)
		return
	}

	estimatedEmissions := big.NewInt(10000)
	circuit := &AppCircuit{EmissionsData: estimatedEmissions}

	circuitInput, err := app.BuildCircuitInput(circuit)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error building circuit input: %v", err), http.StatusInternalServerError)
		return
	}

	witness, _, err := sdk.NewFullWitness(circuit, circuitInput)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error generating witness: %v", err), http.StatusInternalServerError)
		return
	}

	proof, err := sdk.Prove(nil, nil, witness)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error generating proof: %v", err), http.StatusInternalServerError)
		return
	}

	err = app.SubmitProof(proof)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error submitting proof: %v", err), http.StatusInternalServerError)
		return
	}

	tokenAddress := common.HexToAddress("0xbd2F3813637Ed399D5ddBC2307D3bf4Ab1695B48")
	refundAddress := common.HexToAddress("0x788997cD5b9feAc56d4928539Dc21C637C61E69a")

	_, requestId, feeValue, _, err := app.PrepareRequest(
		nil, witness, 11155111, 11155111, refundAddress, tokenAddress, 500000, nil, "",
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error preparing request: %v", err), http.StatusInternalServerError)
		return
	}

	tx, err := app.WaitFinalProofSubmitted(context.Background())
	if err != nil {
		http.Error(w, fmt.Sprintf("Error waiting for proof submission: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"request_id": requestId.Hex(),
		"fee":        feeValue,
		"transaction": tx.Hex(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/prepare-download", handlePrepareDownload)
	http.HandleFunc("/submit-proof", handleSubmitProof)

	log.Printf("Server running on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
