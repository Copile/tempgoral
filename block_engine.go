package temporal

import (
	"context"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// PredictedBlock represents a predicted future block
type PredictedBlock struct {
	Slot          uint64
	Leader        string
	Probability   float64
	JitoValidated bool
}

// BlockEngineClient extends the base temporal Client with block prediction capabilities
type BlockEngineClient struct {
	*Client
	predictions []PredictedBlock
	mu          sync.RWMutex
}

// NewBlockEngine creates a new BlockEngineClient with enhanced block prediction
func NewBlockEngine(ctx context.Context, region Region, apiKey string) (*BlockEngineClient, error) {
	baseClient := New(ctx, region, apiKey)

	return &BlockEngineClient{
		Client:      baseClient,
		predictions: make([]PredictedBlock, 0),
	}, nil
}

// SendTransactionWithMEV sends a transaction with MEV protection using block prediction
func (c *BlockEngineClient) SendTransactionWithMEV(tx *solana.Transaction) (solana.Signature, error) {
	// Get next block prediction
	predictions := c.getPredictions()
	if len(predictions) == 0 {
		// Fallback to regular send if no predictions available
		return c.SendTransaction(tx)
	}

	// Find optimal block for transaction
	optimalBlock := c.findOptimalBlock(predictions)
	if optimalBlock == nil {
		return c.SendTransaction(tx)
	}

	// Add tip instruction for MEV protection if needed
	if !c.hasExistingTip(tx) {
		// Create a new transaction with the tip
		newTx := solana.NewTransaction(
			tx.Message.AccountKeys[0],
			append(
				tx.Message.Instructions,
				solana.NewInstruction(
					solana.SystemProgramID,
					[]byte{2, 0, 0, 0}, // Transfer instruction
					[]solana.AccountMeta{
						{PublicKey: tx.Message.AccountKeys[0], IsSigner: true, IsWritable: true},
						{PublicKey: solana.MustPublicKeyFromBase58(PickRandomNozomiTipAddress()), IsSigner: false, IsWritable: true},
					},
				),
			)...,
		)
		tx = newTx
	}

	// Wait for optimal block
	c.waitForBlock(optimalBlock.Slot)

	return c.SendTransaction(tx)
}

func (c *BlockEngineClient) getPredictions() []PredictedBlock {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.predictions
}

func (c *BlockEngineClient) findOptimalBlock(predictions []PredictedBlock) *PredictedBlock {
	var bestBlock *PredictedBlock
	var highestScore float64

	for i, block := range predictions {
		if block.JitoValidated && block.Probability > highestScore {
			bestBlock = &predictions[i]
			highestScore = block.Probability
		}
	}

	return bestBlock
}

func (c *BlockEngineClient) hasExistingTip(tx *solana.Transaction) bool {
	for _, inst := range tx.Message.Instructions {
		// Check if instruction is a transfer to known tip addresses
		programID := tx.Message.AccountKeys[inst.ProgramIDIndex]
		if programID == solana.SystemProgramID {
			for _, tipAddr := range NOZOMI_TIP_ADDRESSES {
				if tx.Message.AccountKeys[inst.Accounts[1]] == solana.MustPublicKeyFromBase58(tipAddr) {
					return true
				}
			}
		}
	}
	return false
}

func (c *BlockEngineClient) waitForBlock(targetSlot uint64) {
	for {
		slot, err := c.Client.Client.GetSlot(c.Ctx, rpc.CommitmentFinalized)
		if err != nil || uint64(slot) >= targetSlot {
			return
		}
		time.Sleep(time.Millisecond * 50)
	}
}
