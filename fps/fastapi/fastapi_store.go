package fastapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"time"

	"github.com/filecoin-project/go-fil-markets/storagemarket"
	"github.com/ipfs/go-car"
	"github.com/ipfs/go-cid"
	iface "github.com/ipfs/interface-go-ipfs-core"
	"github.com/ipfs/interface-go-ipfs-core/options"
	"github.com/ipfs/interface-go-ipfs-core/path"
	"github.com/klauspost/reedsolomon"
	"github.com/textileio/fil-tools/deals"
	ftypes "github.com/textileio/fil-tools/fps/types"
)

var (
	ErrAlreadyPinned = errors.New("cid already pinned")
	ErrCantPin       = errors.New("couldn't pin data")
	ErrNotStored     = errors.New("cid not stored")

	CantShards = 10
	CantParity = 2
)

func (i *Instance) Put(ctx context.Context, c cid.Cid) error {
	ar := i.auditer.Start(ctx, i.info.ID.String())
	ar.Close()
	if err := i.put(ctx, ar, c); err != nil {
		ar.Errored(err)
		return err
	}
	ar.Success()
	return nil
}

func (i *Instance) put(ctx context.Context, oa ftypes.OpAuditer, c cid.Cid) error {
	// ToDo: register put start for tracking
	_, ok, err := i.getCidInfo(c)
	if err != nil {
		return fmt.Errorf("getting cid %s information: %s", c, err)
	}
	if ok {
		return ErrAlreadyPinned
	}

	cinfo := CidInfo{
		Cid:     c,
		Created: time.Now(),
	}

	cinfo.Hot, err = i.pinToHotLayer(ctx, c)
	if err != nil {
		log.Errorf("pinning cid %s: %s", c, err)
		return ErrCantPin // ToDo: change when going async
	}

	cinfo.Cold, err = i.storeInFIL(ctx, c)
	if err != nil {
		log.Errorf("storing in FIL, cid %s: %s", c, err)
		return err // ToDo: change when going async
	}

	if err := i.saveCidInfo(cinfo); err != nil {
		return fmt.Errorf("saving cid info %v:  %s", cinfo, err)
	}

	return nil
}

func (i *Instance) storeInFIL(ctx context.Context, c cid.Cid) (ColdInfo, error) {
	var ci ColdInfo
	ci.Filecoin = FilInfo{
		Duration: uint64(1000), // ToDo: will evolve as instance/methdo-call has more config
	}

	config, err := makeStorageConfig(ctx, i.ms) // ToDo: will evolve as instance/methdo-call has more config
	if err != nil {
		return ci, fmt.Errorf("selecting miners to make the deal: %s", err)
	}
	rs, size := ipldToFileTransform(ctx, i.ipfs.Dag(), c)
	for k, r := range rs {
		dataCid, result, err := i.dm.Store(ctx, i.info.WalletAddr, r, config, ci.Filecoin.Duration)
		if err != nil {
			return ci, err
		}
		ci.Filecoin.CarSize = size

		notDone := make(map[cid.Cid]struct{})
		propcids := make([]cid.Cid, len(result))
		for i, d := range result {
			ci.Filecoin.Proposals = append(ci.Filecoin.Proposals, FilStorage{
				ProposalCid: d.ProposalCid,
				Failed:      !d.Success,
				ShardNumber: k,
				ShardCid:    dataCid,
			})
			propcids[i] = d.ProposalCid
			notDone[d.ProposalCid] = struct{}{}
		}
		fmt.Printf("saving shard number %d with cid %s\n", k, dataCid)

		chDi, err := i.dm.Watch(ctx, propcids)

		for di := range chDi {
			if di.StateID == storagemarket.StorageDealActive {
				delete(notDone, di.ProposalCid)
			}
			if len(notDone) == 0 {
				break
			}
		}
	}
	return ci, nil
}

func (i *Instance) pinToHotLayer(ctx context.Context, c cid.Cid) (HotInfo, error) {
	var hi HotInfo
	pth := path.IpfsPath(c)
	if err := i.ipfs.Pin().Add(ctx, pth, options.Pin.Recursive(true)); err != nil {
		return hi, err
	}
	stat, err := i.ipfs.Block().Stat(ctx, pth)
	if err != nil {
		return hi, err
	}
	hi.Size = stat.Size()
	hi.Ipfs = IpfsHotInfo{
		Created: time.Now(),
	}
	return hi, nil
}

func ipldToFileTransform(ctx context.Context, dag iface.APIDagService, c cid.Cid) ([]io.Reader, int) {
	r, w := io.Pipe()
	go func() {
		if err := car.WriteCar(ctx, dag, []cid.Cid{c}, w); err != nil {
			w.CloseWithError(err)
		}
		w.Close()
	}()
	all, err := ioutil.ReadAll(r)
	checkErr(err)
	fmt.Println("Size of total data: ", len(all))

	enc, err := reedsolomon.New(CantShards, CantParity)
	checkErr(err)
	shards, err := enc.Split(all)
	checkErr(err)
	fmt.Printf("File split into %d data+parity shards with %d bytes/shard.\n", len(shards), len(shards[0]))
	err = enc.Encode(shards)
	checkErr(err)

	readers := make([]io.Reader, len(shards))
	for i, s := range shards {
		readers[i] = bytes.NewReader(s)
	}

	return readers, len(all)
}

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}

func makeStorageConfig(ctx context.Context, ms ftypes.MinerSelector) ([]deals.StorageDealConfig, error) {
	mps, err := ms.GetTopMiners(1) // ToDo: hardcoded 1 will change when config will be added to instance/method-call
	if err != nil {
		return nil, err
	}
	res := make([]deals.StorageDealConfig, len(mps))
	for i, m := range mps {
		res[i] = deals.StorageDealConfig{Miner: m.Addr, EpochPrice: m.EpochPrice}
	}
	return res, nil
}
