package beacon

import (
	"bytes"
	"errors"
	"time"

	"github.com/protolambda/zrnt/eth2/beacon/common"
	"github.com/protolambda/ztyp/codec"
	"github.com/protolambda/ztyp/tree"
	"github.com/zen-eth/shisui/portalwire"
	"github.com/zen-eth/shisui/storage"
	"github.com/zen-eth/shisui/types/beacon"
)

const GenesisTime uint64 = 1606824023

type ConsensusAPI interface {
	GetBootstrap(blockRoot common.Root) (common.SpecObj, error)
	GetUpdates(firstPeriod, count uint64) ([]common.SpecObj, error)
	GetFinalityUpdate() (common.SpecObj, error)
	GetOptimisticUpdate() (common.SpecObj, error)
	ChainID() uint64
	Name() string
}

var _ ConsensusAPI = &PortalLightApi{}

type PortalLightApi struct {
	portalProtocol *portalwire.PortalProtocol
	spec           *common.Spec
}

func NewPortalLightApi(p *portalwire.PortalProtocol, spec *common.Spec) *PortalLightApi {
	return &PortalLightApi{
		portalProtocol: p,
		spec:           spec,
	}
}

// ChainID implements ConsensusAPI.
func (p *PortalLightApi) ChainID() uint64 {
	return 1
}

// GetBootstrap implements ConsensusAPI.
func (p *PortalLightApi) GetBootstrap(blockRoot tree.Root) (common.SpecObj, error) {
	bootstrapKey := &beacon.LightClientBootstrapKey{
		BlockHash: blockRoot[:],
	}
	contentKeyBytes, err := bootstrapKey.MarshalSSZ()
	if err != nil {
		return nil, err
	}
	contentKey := storage.NewContentKey(LightClientBootstrap, contentKeyBytes).Encode()
	// Get from local
	contentId := p.portalProtocol.ToContentId(contentKey)
	res, err := p.getContent(contentKey, contentId)
	if err != nil {
		return nil, err
	}
	forkedLightClientBootstrap := &beacon.ForkedLightClientBootstrap{}
	err = forkedLightClientBootstrap.Deserialize(p.spec, codec.NewDecodingReader(bytes.NewReader(res), uint64(len(res))))
	if err != nil {
		return nil, err
	}
	return forkedLightClientBootstrap.Bootstrap, nil
}

// GetFinalityUpdate implements ConsensusAPI.
func (p *PortalLightApi) GetFinalityUpdate() (common.SpecObj, error) {
	// Get the finality update for the most recent finalized epoch. We use 0 as the finalized
	// slot because the finalized slot is not known at this point and the protocol is
	// designed to return the most recent which is > 0
	finUpdateKey := &beacon.LightClientFinalityUpdateKey{
		FinalizedSlot: 0,
	}
	contentKeyBytes, err := finUpdateKey.MarshalSSZ()
	if err != nil {
		return nil, err
	}
	contentKey := storage.NewContentKey(LightClientFinalityUpdate, contentKeyBytes).Encode()
	// Get from local
	contentId := p.portalProtocol.ToContentId(contentKey)
	res, err := p.getContent(contentKey, contentId)
	if err != nil {
		return nil, err
	}
	finalityUpdate := &beacon.ForkedLightClientFinalityUpdate{}
	err = finalityUpdate.Deserialize(p.spec, codec.NewDecodingReader(bytes.NewReader(res), uint64(len(res))))
	if err != nil {
		return nil, err
	}
	return finalityUpdate.LightClientFinalityUpdate, nil
}

// GetOptimisticUpdate implements ConsensusAPI.
func (p *PortalLightApi) GetOptimisticUpdate() (common.SpecObj, error) {
	currentSlot := p.spec.TimeToSlot(common.Timestamp(time.Now().Unix()), common.Timestamp(GenesisTime))
	optimisticUpdateKey := &beacon.LightClientOptimisticUpdateKey{
		OptimisticSlot: uint64(currentSlot),
	}
	contentKeyBytes, err := optimisticUpdateKey.MarshalSSZ()
	if err != nil {
		return nil, err
	}
	contentKey := storage.NewContentKey(LightClientOptimisticUpdate, contentKeyBytes).Encode()
	// Get from local
	contentId := p.portalProtocol.ToContentId(contentKey)
	res, err := p.getContent(contentKey, contentId)
	if err != nil {
		return nil, err
	}
	optimisticUpdate := &beacon.ForkedLightClientOptimisticUpdate{}
	err = optimisticUpdate.Deserialize(p.spec, codec.NewDecodingReader(bytes.NewReader(res), uint64(len(res))))
	if err != nil {
		return nil, err
	}
	return optimisticUpdate.LightClientOptimisticUpdate, nil
}

// GetUpdates implements ConsensusAPI.
func (p *PortalLightApi) GetUpdates(firstPeriod uint64, count uint64) ([]common.SpecObj, error) {
	lightClientUpdateKey := &beacon.LightClientUpdateKey{
		StartPeriod: firstPeriod,
		Count:       count,
	}
	contentKeyBytes, err := lightClientUpdateKey.MarshalSSZ()
	if err != nil {
		return nil, err
	}
	contentKey := storage.NewContentKey(LightClientUpdate, contentKeyBytes).Encode()
	// Get from local
	contentId := p.portalProtocol.ToContentId(contentKey)
	data, err := p.getContent(contentKey, contentId)
	if err != nil {
		return nil, err
	}
	var lightClientUpdateRange beacon.LightClientUpdateRange = make([]beacon.ForkedLightClientUpdate, 0)
	err = lightClientUpdateRange.Deserialize(p.spec, codec.NewDecodingReader(bytes.NewReader(data), uint64(len(data))))
	if err != nil {
		return nil, err
	}
	res := make([]common.SpecObj, len(lightClientUpdateRange))

	for i, item := range lightClientUpdateRange {
		res[i] = item.LightClientUpdate
	}
	return res, nil
}

// Name implements ConsensusAPI.
func (p *PortalLightApi) Name() string {
	return "portal"
}

func (p *PortalLightApi) getContent(contentKey, contentId []byte) ([]byte, error) {
	res, err := p.portalProtocol.Get(contentKey, contentId)
	// other error
	if err != nil && !errors.Is(err, storage.ErrContentNotFound) {
		return nil, err
	}
	if errors.Is(err, storage.ErrContentNotFound) {
		// Get from remote
		res, _, err = p.portalProtocol.ContentLookup(contentKey, contentId)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}
