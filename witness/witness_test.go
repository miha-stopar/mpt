package witness

import (
	"fmt"
	"log"
	"math/big"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/miha-stopar/mpt/oracle"
	"github.com/miha-stopar/mpt/state"
	"github.com/miha-stopar/mpt/trie"
)

const branchRLPOffset = 2
const branch2start = branchRLPOffset + 32
const rowLen = branch2start + branchRLPOffset + 32 + 1 // +1 is for info about what type of row is it

/*
Info about row type (given as the last element of the row):
0: init branch (such a row contains RLP info about the branch node; key)
1: branch child
2: leaf s
3: leaf c
4: leaf key s
5: leaf key c
*/

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func matrixToJson(rows [][]byte) string {
	// Had some problems with json.Marshal, so I just prepare json manually.
	json := "["
	for i := 0; i < len(rows); i++ {
		json += listToJson(rows[i])
		if i != len(rows)-1 {
			json += ","
		}
	}
	json += "]"

	return json
}

func listToJson(row []byte) string {
	json := "["
	for j := 0; j < len(row); j++ {
		json += strconv.Itoa(int(row[j]))
		if j != len(row)-1 {
			json += ","
		}
	}
	json += "]"

	return json
}

func VerifyProof(proof [][]byte, key []byte) bool {
	hasher := trie.NewHasher(false)
	for i := 0; i < len(proof)-1; i++ {
		parentHash := hasher.HashData(proof[i])
		parent, err := trie.DecodeNode(parentHash, proof[i])
		check(err)

		childHash := hasher.HashData(proof[i+1])
		child, err := trie.DecodeNode(childHash, proof[i+1])
		check(err)

		r := parent.(*trie.FullNode)
		c := r.Children[key[i]] // TODO: doesn't cover all scenarios
		u, _ := hasher.Hash(child, false)

		if fmt.Sprintf("%b", u) != fmt.Sprintf("%b", c) {
			return false
		}
	}

	return true
}

func VerifyTwoProofsAndPath(proof1, proof2 [][]byte, key []byte) bool {
	if len(proof1) != len(proof2) {
		fmt.Println("constraint failed: proofs length not the same")
		return false
	}
	hasher := trie.NewHasher(false)
	for i := 0; i < len(proof1)-2; i++ { // -2 because the last element is leaf key (not RLP)
		parentHash := hasher.HashData(proof1[i])
		parent, err := trie.DecodeNode(parentHash, proof1[i])
		check(err)

		childHash := hasher.HashData(proof1[i+1])
		child, err := trie.DecodeNode(childHash, proof1[i+1])
		check(err)

		r := parent.(*trie.FullNode)
		c := r.Children[key[i]] // TODO: doesn't cover all scenarios
		u, _ := hasher.Hash(child, false)

		if fmt.Sprintf("%b", u) != fmt.Sprintf("%b", c) {
			fmt.Println("constraint failed: proof not valid")
			return false
		}

		parentHash2 := hasher.HashData(proof2[i])
		parent2, err := trie.DecodeNode(parentHash2, proof2[i])
		check(err)

		childHash2 := hasher.HashData(proof2[i+1])
		child2, err := trie.DecodeNode(childHash2, proof2[i+1])
		check(err)

		r2 := parent2.(*trie.FullNode)
		c2 := r2.Children[key[i]] // TODO: doesn't cover all scenarios
		u2, _ := hasher.Hash(child2, false)

		if fmt.Sprintf("%b", u2) != fmt.Sprintf("%b", c2) {
			fmt.Println("constraint failed: proof not valid")
			return false
		}

		// Constraints that we are having the same path for both proofs:
		for j := 0; j < 16; j++ {
			if j != int(key[i]) {
				if fmt.Sprintf("%b", r.Children[j]) != fmt.Sprintf("%b", r2.Children[j]) {
					fmt.Println("constraint failed: path not valid")
					return false
				}
			}
		}
	}

	return true
}

// Check that elements in a branch are all the same, except at the position exceptPos.
func VerifyElementsInTwoBranches(b1, b2 *trie.FullNode, exceptPos byte) bool {
	for j := 0; j < 16; j++ {
		if j != int(exceptPos) {
			if fmt.Sprintf("%b", b1.Children[j]) != fmt.Sprintf("%b", b2.Children[j]) {
				fmt.Println("constraint failed: element in branch not the same")
				return false
			}
		}
	}
	return true
}

func prepareBranchWitness(rows [][]byte, branch []byte, branchStart int) {
	rowInd := 1 // start with 1 because rows[0] contains some RLP data
	colInd := branchRLPOffset
	inside32Ind := -1
	for i := 0; i < int(branch[1]); i++ { // TODO: length can occupy more than just one byte
		if rowInd == 17 {
			break
		}
		b := branch[branchRLPOffset+i]
		if b == 160 && inside32Ind == -1 { // new child
			inside32Ind = 0
			colInd = branchRLPOffset - 1
			rows[rowInd][branchStart+colInd] = b
			colInd++
			continue
		}

		if inside32Ind >= 0 {
			rows[rowInd][branchStart+colInd] = b
			colInd++
			inside32Ind++
			fmt.Println(rows[rowInd])
			if inside32Ind == 32 {
				inside32Ind = -1
				rowInd++
				colInd = 0
			}
		} else {
			// if we are not in a child, it can only be b = 128 which presents nil (no child
			// at this position)
			if b != 128 {
				panic("not 128")
			}
			rows[rowInd][branchStart+branchRLPOffset] = b
			rowInd++
			fmt.Println(rows[rowInd-1])
		}
	}
}

func prepareLeaf(row []byte, typ byte) []byte {
	// Avoid directly changing the row as it might introduce some bugs later on.
	leaf := make([]byte, len(row))
	copy(leaf, row)
	leaf = append(leaf, typ)

	return leaf
}

func prepareTwoBranchesWitness(branch1, branch2 []byte, key byte) [][]byte {
	rows := make([][]byte, 17)
	rows[0] = make([]byte, rowLen)

	// Let's put in the 0-th row some RLP data (the length of the whole branch RLP)
	// TODO: this can occupy more than two bytes
	rows[0][0] = branch1[0]
	rows[0][1] = branch1[1]
	rows[0][2] = branch2[0]
	rows[0][3] = branch2[1]
	rows[0][4] = key

	for i := 1; i < 17; i++ {
		rows[i] = make([]byte, rowLen)
		if i == 0 {
			rows[i][branch2start+branchRLPOffset+32+1-1] = 0
		} else {
			rows[i][branch2start+branchRLPOffset+32+1-1] = 1
		}
	}
	prepareBranchWitness(rows, branch1, 0)
	prepareBranchWitness(rows, branch2, 2+32)

	return rows
}

func prepareWitness(storageProof, storageProof1 [][]byte, key []byte) [][]byte {
	rows := make([][]byte, 0)
	for i := 0; i < len(storageProof); i++ {
		if i == len(storageProof)-1 {
			l := make([]byte, len(storageProof[i]))
			copy(l, storageProof[i])
			l = append(l, 4) // 4 is leaf key s
			rows = append(rows, l)

			l1 := make([]byte, len(storageProof1[i]))
			copy(l1, storageProof1[i])
			l1 = append(l1, 5) // 5 is leaf key c
			rows = append(rows, l1)

			return rows
		}
		elems, _, err := rlp.SplitList(storageProof[i])
		if err != nil {
			fmt.Println("decode error", err)
		}
		switch c, _ := rlp.CountValues(elems); c {
		case 2:
			leaf1 := prepareLeaf(storageProof[i], 2)  // leaf s
			leaf2 := prepareLeaf(storageProof1[i], 3) // leaf c
			rows = append(rows, leaf1)
			rows = append(rows, leaf2)
		case 17:
			bRows := prepareTwoBranchesWitness(storageProof[i], storageProof1[i], key[i])
			rows = append(rows, bRows...)
			// check
			for k := 1; k < 17; k++ {
				if k-1 == int(key[i]) {
					continue
				}
				for j := 0; j < branchRLPOffset+32; j++ {
					if bRows[k][j] != bRows[k][branch2start+j] {
						panic("witness not properly generated")
					}
				}
			}
		default:
			fmt.Println("invalid number of list elements")
		}
	}

	return rows
}

func execTest(keys []common.Hash, toBeModified common.Hash) {
	blockNum := 13284469
	blockNumberParent := big.NewInt(int64(blockNum))
	blockHeaderParent := oracle.PrefetchBlock(blockNumberParent, true, nil)
	database := state.NewDatabase(blockHeaderParent)
	statedb, _ := state.New(blockHeaderParent.Root, database, nil)
	addr := common.HexToAddress("0x50efbf12580138bc263c95757826df4e24eb81c9")

	for i := 0; i < len(keys); i++ {
		k := keys[i]
		v := common.BigToHash(big.NewInt(int64(i + 1))) // don't put 0 value because otherwise nothing will be set (if 0 is prev value), see state_object.go line 279
		statedb.SetState(addr, k, v)
	}

	// Let's say above state is our starting position.
	storageProof, err := statedb.GetStorageProof(addr, toBeModified)
	check(err)

	kh := crypto.Keccak256(toBeModified.Bytes())
	key := trie.KeybytesToHex(kh)

	/*
		Modifying storage:
	*/

	// We now change one existing storage slot:
	v := common.BigToHash(big.NewInt(int64(17)))
	statedb.SetState(addr, toBeModified, v)

	// We ask for a proof for the modified slot:
	statedb.IntermediateRoot(false)
	storageProof1, err := statedb.GetStorageProof(addr, toBeModified)
	check(err)

	rows := prepareWitness(storageProof, storageProof1, key)
	fmt.Println(matrixToJson(rows))

	if !VerifyTwoProofsAndPath(storageProof, storageProof1, key) {
		panic("proof not valid")
	}
}

func TestStorageUpdateOneLevel(t *testing.T) {
	ks := [...]common.Hash{common.HexToHash("0x12"), common.HexToHash("0x21")}
	// hexed keys:
	// [3,1,14,12,12,...
	// [11,11,8,10,6,...
	// We have a branch with children at position 3 and 11.

	toBeModified := ks[0]

	execTest(ks[:], toBeModified)
}

func TestStorageUpdateTwoLevels(t *testing.T) {
	ks := [...]common.Hash{common.HexToHash("0x11"), common.HexToHash("0x12"), common.HexToHash("0x21")} // this has three levels
	// hexed keys:
	// [3,1,14,12,12,...
	// [11,11,8,10,6,...
	// First we have a branch with children at position 3 and 11.
	// The third storage change happens at key:
	// [3,10,6,3,5,7,...
	// That means leaf at position 3 turns into branch with children at position 1 and 10.
	// ks := [...]common.Hash{common.HexToHash("0x12"), common.HexToHash("0x21")}

	toBeModified := ks[0]

	execTest(ks[:], toBeModified)
}

func TestStorageAddOneLevel(t *testing.T) {
	blockNum := 13284469
	blockNumberParent := big.NewInt(int64(blockNum))
	blockHeaderParent := oracle.PrefetchBlock(blockNumberParent, true, nil)

	database := state.NewDatabase(blockHeaderParent)
	statedb, _ := state.New(blockHeaderParent.Root, database, nil)

	addr := common.HexToAddress("0x50efbf12580138bc263c95757826df4e24eb81c9")

	ks := [...]common.Hash{common.HexToHash("0x12"), common.HexToHash("0x21")}
	for i := 0; i < len(ks); i++ {
		k := ks[i]
		v := common.BigToHash(big.NewInt(int64(i + 1))) // don't put 0 value because otherwise nothing will be set (if 0 is prev value), see state_object.go line 279
		statedb.SetState(addr, k, v)
	}
	// We have a branch with two leaves at positions 3 and 11.

	// Let's say above is our starting position.

	// This is a storage slot that will be modified (the list will come from bus-mapping).
	// Compared to the test TestStorageUpdateOneLevel, there is no node in trie for this storage key.
	toBeModified := [...]common.Hash{common.HexToHash("0x31")}

	// We now get a storageProof for the starting position for the slot that will be changed further on (ks[1]):
	// This first storageProof will actually be retrieved by RPC eth_getProof (see oracle.PrefetchStorage function).
	// All other proofs (after modifications) will be generated internally by buildig the internal state.
	storageProof, err := statedb.GetStorageProof(addr, toBeModified[0])
	check(err)
	hasher := trie.NewHasher(false)

	// Compared to the test TestStorageUpdateOneLevel, there is no node in trie for this storage key - the key
	// asks for the position 12 and there is nothing. Thus, the proof will only contain one element - the root node.

	kh := crypto.Keccak256(toBeModified[0].Bytes())
	key := trie.KeybytesToHex(kh)

	rootHash := hasher.HashData(storageProof[0])
	root, err := trie.DecodeNode(rootHash, storageProof[0])
	check(err)
	r := root.(*trie.FullNode)

	// Constraint for proof verification - only one element in the proof so nothing to be verified except
	// that the key at this position is nil:
	if r.Children[key[0]] != nil {
		panic("not correct")
	}

	/*
		Modifying storage:
	*/

	// We now change the storage slot:
	v := common.BigToHash(big.NewInt(int64(17)))
	statedb.SetState(addr, toBeModified[0], v)

	// We ask for a proof for the modified slot:
	statedb.IntermediateRoot(false)
	storageProof2, err := statedb.GetStorageProof(addr, toBeModified[0])
	check(err)

	rootHash2 := hasher.HashData(storageProof2[0])
	root2, err := trie.DecodeNode(rootHash2, storageProof2[0])
	check(err)
	r2 := root2.(*trie.FullNode)

	if !VerifyProof(storageProof2, key) {
		panic("proof not valid")
	}

	if !VerifyElementsInTwoBranches(r, r2, key[0]) {
		panic("proof not valid")
	}
}

func TestStateUpdateOneLevel(t *testing.T) {
	// Here we are checking the whole state trie, not only a storage trie for some account as in above tests.
	blockNum := 13284469
	blockNumberParent := big.NewInt(int64(blockNum))
	blockHeaderParent := oracle.PrefetchBlock(blockNumberParent, true, nil)

	database := state.NewDatabase(blockHeaderParent)
	statedb, _ := state.New(blockHeaderParent.Root, database, nil)

	addr := common.HexToAddress("0x50efbf12580138bc263c95757826df4e24eb81c9")

	ks := [...]common.Hash{common.HexToHash("0x12"), common.HexToHash("0x21")}
	for i := 0; i < len(ks); i++ {
		k := ks[i]
		v := common.BigToHash(big.NewInt(int64(i + 1))) // don't put 0 value because otherwise nothing will be set (if 0 is prev value), see state_object.go line 279
		statedb.SetState(addr, k, v)
	}
	// We have a branch with two leaves at positions 3 and 11.

	// Let's say above is our starting position.

	// This is a storage slot that will be modified (the list will come from bus-mapping):
	toBeModified := [...]common.Hash{ks[1]}

	// We now get a proof for the starting position for the slot that will be changed further on (ks[1]):
	// This first proof will actually be retrieved by RPC eth_getProof (see oracle.PrefetchStorage function).
	// All other proofs (after modifications) will be generated internally by buildig the internal state.

	accountProof, err := statedb.GetProof(addr)
	check(err)
	storageProof, err := statedb.GetStorageProof(addr, toBeModified[0])
	check(err)

	// By calling RPC eth_getProof we will get accountProof and storageProof.

	// The last element in accountProof contains the state object for this address.
	// We need to verify that the state object for this address is the in last
	// element of the accountProof. The last element of the accountProof actually contains the RLP of
	// nonce, balance, code, and root.
	// We need to use a root from the storage proof (first element) and obtain balance, code, and nonce
	// by the following RPC calls:
	// eth_getBalance, eth_getCode, eth_getTransactionCount (nonce).
	// We use these four values to compute the hash and compare it to the last value in accountProof.

	// We simulate getting the RLP of the four values (instead of using RPC calls and taking the first
	// element of the storage proof):
	obj := statedb.GetOrNewStateObject(addr)
	rl, err := rlp.EncodeToBytes(obj)
	check(err)

	hasher := trie.NewHasher(false)

	ind := len(accountProof) - 1
	accountHash := hasher.HashData(accountProof[ind])
	accountLeaf, err := trie.DecodeNode(accountHash, accountProof[ind])
	check(err)

	account := accountLeaf.(*trie.ShortNode)
	accountValueNode := account.Val.(trie.ValueNode)

	// Constraint for checking the transition from storage to account proof:
	if fmt.Sprintf("%b", rl) != fmt.Sprintf("%b", accountValueNode) {
		panic("not the same")
	}

	accountAddr := trie.KeybytesToHex(crypto.Keccak256(addr.Bytes()))

	kh := crypto.Keccak256(toBeModified[0].Bytes())
	key := trie.KeybytesToHex(kh)

	/*
		Modifying storage:
	*/

	// We now change one existing storage slot:
	v := common.BigToHash(big.NewInt(int64(17)))
	statedb.SetState(addr, toBeModified[0], v)

	// We ask for a proof for the modified slot:
	statedb.IntermediateRoot(false)

	accountProof1, err := statedb.GetProof(addr)
	check(err)

	storageProof1, err := statedb.GetStorageProof(addr, toBeModified[0])
	check(err)

	if !VerifyTwoProofsAndPath(accountProof, accountProof1, accountAddr) {
		panic("proof not valid")
	}

	if !VerifyTwoProofsAndPath(storageProof, storageProof1, key) {
		panic("proof not valid")
	}
}
