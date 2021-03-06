package chainstore

import (
	"context"
	"crypto/rand"
	"os"
	"testing"

	"github.com/filecoin-project/lotus/chain/types"
	"github.com/google/go-cmp/cmp"
	"github.com/ipfs/go-cid"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/multiformats/go-multihash"
	"github.com/textileio/powergate/tests"
)

type data struct {
	Tipset string
	Nested extraData
}

type extraData struct {
	Pos int
}

func TestMain(m *testing.M) {
	cbor.RegisterCborType(data{})
	cbor.RegisterCborType(extraData{})
	os.Exit(m.Run())
}

func TestLoadFromEmpty(t *testing.T) {
	ctx := context.Background()
	cs, err := New(tests.NewTxMapDatastore(), newMockTipsetOrderer())
	checkErr(t, err)

	var d data
	target := types.NewTipSetKey(cid.Undef)
	ts, err := cs.LoadAndPrune(ctx, target, &d)
	checkErr(t, err)
	if ts != nil {
		t.Fatal("base tipset should be nil")
	}
	if !cmp.Equal(d, data{}) {
		t.Fatal("state should be default")
	}
}

func TestSaveSingle(t *testing.T) {
	ctx := context.Background()
	mto := newMockTipsetOrderer()
	cs, err := New(tests.NewTxMapDatastore(), mto)
	checkErr(t, err)

	ts, v := mto.next()
	err = cs.Save(ctx, ts, &v)
	checkErr(t, err)

	var v2 data
	bts, err := cs.LoadAndPrune(ctx, ts, &v2)
	checkErr(t, err)
	if !cmp.Equal(v, v2) || *bts != ts {
		t.Fatalf("saved and loaded state from same tipset should be equal")
	}
}

func TestSaveMultiple(t *testing.T) {
	ctx := context.Background()
	mto := newMockTipsetOrderer()
	cs, err := New(tests.NewTxMapDatastore(), mto)
	checkErr(t, err)

	generateTotal := 100
	for i := 0; i < generateTotal; i++ {
		ts, v := mto.next()
		err := cs.Save(ctx, ts, &v)
		checkErr(t, err)
	}

	// Check that we're capping # of checkpoints to maxCheckpoints
	if len(cs.checkpoints) != maxCheckpoints {
		t.Fatalf("there should be exactly maxCheckpoints saved")
	}
	// Check saved ones are the last maxCheckpoint ones
	expectedTipsets := mto.list[generateTotal-maxCheckpoints:]
	for i, c := range cs.checkpoints {
		if c.ts != expectedTipsets[i] {
			t.Fatalf("saved tipset doesn't seem to correspond with expected one")
		}
	}

	for i := len(expectedTipsets) - 1; i >= 0; i-- {
		ts := expectedTipsets[i]
		var v data
		bts, err := cs.LoadAndPrune(ctx, ts, &v)
		checkErr(t, err)
		if *bts != ts || v.Nested.Pos != generateTotal-maxCheckpoints+i {
			t.Fatalf("elem %d doesn't seem to be loaded from correct tipset", i)
		}
	}
}

func TestSaveInvalid(t *testing.T) {
	ctx := context.Background()
	mto := newMockTipsetOrderer()
	cs, err := New(tests.NewTxMapDatastore(), mto)
	checkErr(t, err)

	ts1, v1 := mto.next()
	ts2, v2 := mto.next()

	err = cs.Save(ctx, ts2, &v2)
	checkErr(t, err)

	err = cs.Save(ctx, ts1, &v1)
	if err == nil {
		t.Fatalf("Save shouldn't allow to save state on an older tipset that last known")
	}
}

// Most interesting test.
// Create 10 happy-chain tipset saves. Load from new tipset (tsFork) that forks from
// 6th saved tipset. The Load should delete checkpoints 7, 8, 9 and 10 since tsFork
// doesnt Precede() from any of them, and return state of checkpoint 6.
// Saying it differently, Load should return the last state from the most recent
// checkpoint that precedes the target tipset.
func TestLoadForkedCheckpoint(t *testing.T) {
	ctx := context.Background()
	mto := newMockTipsetOrderer()
	cs, err := New(tests.NewTxMapDatastore(), mto)
	checkErr(t, err)

	for i := 0; i < 10; i++ {
		ts, v := mto.next()
		err := cs.Save(ctx, ts, &v)
		checkErr(t, err)
	}

	fts := mto.fork(5)
	var v data
	bts, err := cs.LoadAndPrune(ctx, fts, &v)
	checkErr(t, err)

	if *bts != mto.list[5] {
		t.Fatalf("returned base tipset state should be from the 6th checkpoint")
	}
	if v.Nested.Pos != 5 {
		t.Fatalf("state return doesn't seem to correspond to the 6th checkpoint")
	}
}

func TestLoadSavedState(t *testing.T) {
	ctx := context.Background()
	mto := newMockTipsetOrderer()
	ds := tests.NewTxMapDatastore()
	cs, err := New(ds, mto)
	checkErr(t, err)

	generateTotal := 100
	for i := 0; i < generateTotal; i++ {
		ts, v := mto.next()
		err := cs.Save(ctx, ts, &v)
		checkErr(t, err)
	}

	cs, err = New(ds, mto)
	checkErr(t, err)
	if len(cs.checkpoints) != maxCheckpoints {
		t.Fatalf("checkpoints are missing")
	}

	offset := 3
	savedTipset := mto.list[len(mto.list)-offset]
	var v data
	bts, err := cs.LoadAndPrune(ctx, savedTipset, &v)
	checkErr(t, err)
	if *bts != savedTipset || v.Tipset != savedTipset.String() || v.Nested.Pos != generateTotal-offset {
		t.Fatalf("returned state is wrong")
	}

}

type mockTipsetOrderer struct {
	forks map[string]string
	list  []types.TipSetKey
}

func newMockTipsetOrderer() *mockTipsetOrderer {
	return &mockTipsetOrderer{
		forks: make(map[string]string),
	}
}

func (mto *mockTipsetOrderer) Precedes(ctx context.Context, from, to types.TipSetKey) (bool, error) {
	if forkedTs, ok := mto.forks[from.String()]; ok {
		if forkedTs == to.String() {
			return true, nil
		}
	}

	var foundFrom bool
	for _, v := range mto.list {
		foundFrom = foundFrom || from == v
		if foundFrom && to == v {
			return true, nil
		}
	}
	return false, nil
}

func (mto *mockTipsetOrderer) next() (types.TipSetKey, data) {
	ts := randomTipsetkey()
	mto.list = append(mto.list, ts)

	return ts, data{Tipset: ts.String(), Nested: extraData{
		Pos: len(mto.list) - 1,
	}}
}

func (mto *mockTipsetOrderer) fork(i int) types.TipSetKey {
	fork := randomTipsetkey()
	mto.forks[mto.list[i].String()] = fork.String()
	return fork
}

func randomTipsetkey() types.TipSetKey {
	r := make([]byte, 16)
	_, err := rand.Read(r)
	if err != nil {
		panic(err)
	}
	mh, err := multihash.Sum(r, multihash.IDENTITY, -1)
	if err != nil {
		panic(err)
	}
	return types.NewTipSetKey(cid.NewCidV1(cid.Raw, mh))
}

func checkErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
