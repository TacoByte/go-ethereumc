package chain

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/chain/types"
	"github.com/ethereum/go-ethereum/ethutil"
	"github.com/ethereum/go-ethereum/logger"
)

var chainlogger = logger.NewLogger("CHAIN")

func AddTestNetFunds(block *types.Block) {
	for _, addr := range []string{
		"51ba59315b3a95761d0863b05ccc7a7f54703d99",
		"e4157b34ea9615cfbde6b4fda419828124b70c78",
		"b9c015918bdaba24b4ff057a92a3873d6eb201be",
		"6c386a4b26f73c802f34673f7248bb118f97424a",
		"cd2a3d9f938e13cd947ec05abc7fe734df8dd826",
		"2ef47100e0787b915105fd5e3f4ff6752079d5cb",
		"e6716f9544a56c530d868e4bfbacb172315bdead",
		"1a26338f0d905e295fccb71fa9ea849ffa12aaf4",
	} {
		codedAddr := ethutil.Hex2Bytes(addr)
		account := block.State().GetAccount(codedAddr)
		account.SetBalance(ethutil.Big("1606938044258990275541962092341162602522202993782792835301376")) //ethutil.BigPow(2, 200)
		block.State().UpdateStateObject(account)
	}
}

func CalcDifficulty(block, parent *types.Block) *big.Int {
	diff := new(big.Int)

	adjust := new(big.Int).Rsh(parent.Difficulty, 10)
	if block.Time >= parent.Time+5 {
		diff.Sub(parent.Difficulty, adjust)
	} else {
		diff.Add(parent.Difficulty, adjust)
	}

	return diff
}

type ChainManager struct {
	//eth          EthManager
	processor    types.BlockProcessor
	genesisBlock *types.Block
	// Last known total difficulty
	TD *big.Int

	LastBlockNumber uint64

	CurrentBlock  *types.Block
	LastBlockHash []byte
}

func NewChainManager() *ChainManager {
	bc := &ChainManager{}
	bc.genesisBlock = types.NewBlockFromBytes(ethutil.Encode(Genesis))
	//bc.eth = ethereum

	bc.setLastBlock()

	return bc
}

func (self *ChainManager) SetProcessor(proc types.BlockProcessor) {
	self.processor = proc
}

func (bc *ChainManager) setLastBlock() {
	data, _ := ethutil.Config.Db.Get([]byte("LastBlock"))
	if len(data) != 0 {
		// Prep genesis
		AddTestNetFunds(bc.genesisBlock)

		block := types.NewBlockFromBytes(data)
		bc.CurrentBlock = block
		bc.LastBlockHash = block.Hash()
		bc.LastBlockNumber = block.Number.Uint64()

		// Set the last know difficulty (might be 0x0 as initial value, Genesis)
		bc.TD = ethutil.BigD(ethutil.Config.Db.LastKnownTD())
	} else {
		bc.Reset()
	}

	chainlogger.Infof("Last block (#%d) %x\n", bc.LastBlockNumber, bc.CurrentBlock.Hash())
}

// Block creation & chain handling
func (bc *ChainManager) NewBlock(coinbase []byte) *types.Block {
	var root interface{}
	hash := ZeroHash256

	if bc.CurrentBlock != nil {
		root = bc.CurrentBlock.Root()
		hash = bc.LastBlockHash
	}

	block := types.CreateBlock(
		root,
		hash,
		coinbase,
		ethutil.BigPow(2, 32),
		nil,
		"")

	parent := bc.CurrentBlock
	if parent != nil {
		block.Difficulty = CalcDifficulty(block, parent)
		block.Number = new(big.Int).Add(bc.CurrentBlock.Number, ethutil.Big1)
		block.GasLimit = block.CalcGasLimit(bc.CurrentBlock)

	}

	return block
}

func (bc *ChainManager) Reset() {
	AddTestNetFunds(bc.genesisBlock)

	bc.genesisBlock.Trie().Sync()
	// Prepare the genesis block
	bc.add(bc.genesisBlock)
	bc.CurrentBlock = bc.genesisBlock

	bc.SetTotalDifficulty(ethutil.Big("0"))

	// Set the last know difficulty (might be 0x0 as initial value, Genesis)
	bc.TD = ethutil.BigD(ethutil.Config.Db.LastKnownTD())
}

// Add a block to the chain and record addition information
func (bc *ChainManager) add(block *types.Block) {
	bc.writeBlockInfo(block)

	bc.CurrentBlock = block
	bc.LastBlockHash = block.Hash()

	encodedBlock := block.RlpEncode()
	ethutil.Config.Db.Put(block.Hash(), encodedBlock)
	ethutil.Config.Db.Put([]byte("LastBlock"), encodedBlock)

	//chainlogger.Infof("Imported block #%d (%x...)\n", block.Number, block.Hash()[0:4])
}

// Accessors
func (bc *ChainManager) Genesis() *types.Block {
	return bc.genesisBlock
}

// Block fetching methods
func (bc *ChainManager) HasBlock(hash []byte) bool {
	data, _ := ethutil.Config.Db.Get(hash)
	return len(data) != 0
}

func (self *ChainManager) GetChainHashesFromHash(hash []byte, max uint64) (chain [][]byte) {
	block := self.GetBlock(hash)
	if block == nil {
		return
	}

	// XXX Could be optimised by using a different database which only holds hashes (i.e., linked list)
	for i := uint64(0); i < max; i++ {

		chain = append(chain, block.Hash())

		if block.Number.Cmp(ethutil.Big0) <= 0 {
			break
		}

		block = self.GetBlock(block.PrevHash)
	}

	return
}

func (self *ChainManager) GetBlock(hash []byte) *types.Block {
	data, _ := ethutil.Config.Db.Get(hash)
	if len(data) == 0 {
		return nil
	}

	return types.NewBlockFromBytes(data)
}

func (self *ChainManager) GetBlockByNumber(num uint64) *types.Block {
	block := self.CurrentBlock
	for ; block != nil; block = self.GetBlock(block.PrevHash) {
		if block.Number.Uint64() == num {
			break
		}
	}

	if block != nil && block.Number.Uint64() == 0 && num != 0 {
		return nil
	}

	return block
}

func (bc *ChainManager) SetTotalDifficulty(td *big.Int) {
	ethutil.Config.Db.Put([]byte("LTD"), td.Bytes())
	bc.TD = td
}

func (self *ChainManager) CalcTotalDiff(block *types.Block) (*big.Int, error) {
	parent := self.GetBlock(block.PrevHash)
	if parent == nil {
		return nil, fmt.Errorf("Unable to calculate total diff without known parent %x", block.PrevHash)
	}

	parentTd := parent.BlockInfo().TD

	uncleDiff := new(big.Int)
	for _, uncle := range block.Uncles {
		uncleDiff = uncleDiff.Add(uncleDiff, uncle.Difficulty)
	}

	td := new(big.Int)
	td = td.Add(parentTd, uncleDiff)
	td = td.Add(td, block.Difficulty)

	return td, nil
}

func (bc *ChainManager) BlockInfo(block *types.Block) types.BlockInfo {
	bi := types.BlockInfo{}
	data, _ := ethutil.Config.Db.Get(append(block.Hash(), []byte("Info")...))
	bi.RlpDecode(data)

	return bi
}

// Unexported method for writing extra non-essential block info to the db
func (bc *ChainManager) writeBlockInfo(block *types.Block) {
	bc.LastBlockNumber++
	bi := types.BlockInfo{Number: bc.LastBlockNumber, Hash: block.Hash(), Parent: block.PrevHash, TD: bc.TD}

	// For now we use the block hash with the words "info" appended as key
	ethutil.Config.Db.Put(append(block.Hash(), []byte("Info")...), bi.RlpEncode())
}

func (bc *ChainManager) Stop() {
	if bc.CurrentBlock != nil {
		chainlogger.Infoln("Stopped")
	}
}

func (self *ChainManager) InsertChain(chain Blocks) error {
	for _, block := range chain {
		td, messages, err := self.Ethereum.BlockManager().Process(block)
		if err != nil {
			if IsKnownBlockErr(err) {
				continue
			}

			return err
		}

		self.add(block)
		self.SetTotalDifficulty(td)
		self.Ethereum.EventMux().Post(NewBlockEvent{block})
		self.Ethereum.EventMux().Post(messages)
	}

	return nil
}