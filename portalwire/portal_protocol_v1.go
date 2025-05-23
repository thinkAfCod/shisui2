package portalwire

import (
	"errors"
	"slices"

	bitfield "github.com/OffchainLabs/go-bitfield"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/enr"
)

var ErrUnsupportedVersion = errors.New("unsupported version")

type AcceptCode uint8

const (
	Accepted AcceptCode = iota
	GenericDeclined
	AlreadyStored
	NotWithinRadius
	RateLimited               // rate limit reached. Node can't handle anymore connections
	InboundTransferInProgress // inbound rate limit reached for accepting a specific content_id, used to protect against thundering herds
	Unspecified
)

var (
	EmptyBytes = make([]byte, 0)
)

type CommonAccept interface {
	MarshalSSZ() ([]byte, error)
	UnmarshalSSZ([]byte) error
	GetConnectionId() []byte
	SetConnectionId([]byte)
	GetContentKeys() []byte
	SetContentKeys([]byte)
	GetAcceptIndices() []int
	GetKeyLength() int
}

func (a *Accept) GetConnectionId() []byte {
	return a.ConnectionId
}
func (a *Accept) SetConnectionId(id []byte) {
	a.ConnectionId = id
}
func (a *Accept) GetContentKeys() []byte {
	return a.ContentKeys
}
func (a *Accept) SetContentKeys(keys []byte) {
	a.ContentKeys = keys
}

func (a *Accept) GetKeyLength() int {
	return int(bitfield.Bitlist(a.ContentKeys).Len())
}

func (a *Accept) GetAcceptIndices() []int {
	return bitfield.Bitlist(a.ContentKeys).BitIndices()
}

func (a *AcceptV1) GetConnectionId() []byte {
	return a.ConnectionId
}
func (a *AcceptV1) SetConnectionId(id []byte) {
	a.ConnectionId = id
}
func (a *AcceptV1) GetContentKeys() []byte {
	return a.ContentKeys
}
func (a *AcceptV1) SetContentKeys(keys []byte) {
	a.ContentKeys = keys
}

func (a *AcceptV1) GetAcceptIndices() []int {
	res := make([]int, 0)
	for i, val := range a.ContentKeys {
		if val == uint8(Accepted) {
			res = append(res, i)
		}
	}
	return res
}

func (a *AcceptV1) GetKeyLength() int {
	return len(a.GetContentKeys())
}

func (p *PortalProtocol) getOrStoreHighestVersion(node *enode.Node) (uint8, error) {
	hcVersionValue, ok := p.versionsCache.Get(node)
	if ok {
		return hcVersionValue, nil
	}

	versions := &protocolVersions{}
	err := node.Load(versions)
	// key is not set, return the default version
	if enr.IsNotFound(err) {
		p.versionsCache.Set(node, p.currentVersions[0], 0)
		return p.currentVersions[0], nil
	}
	if err != nil {
		return 0, err
	}

	hcVersion, err := findBiggestSameNumber(p.currentVersions, *versions)
	p.versionsCache.Set(node, hcVersion, 0)
	return hcVersion, err
}

// find the Accept.ContentKeys and the content keys to accept
func (p *PortalProtocol) filterContentKeys(request *Offer, version uint8) (CommonAccept, [][]byte, error) {
	switch version {
	case 0:
		return p.filterContentKeysV0(request)
	case 1:
		return p.filterContentKeysV1(request)
	default:
		return nil, nil, ErrUnsupportedVersion
	}
}

func (p *PortalProtocol) filterContentKeysV0(request *Offer) (CommonAccept, [][]byte, error) {
	contentKeyBitlist := bitfield.NewBitlist(uint64(len(request.ContentKeys)))
	acceptContentKeys := make([][]byte, 0)
	if len(p.contentQueue) < cap(p.contentQueue) {
		for i, contentKey := range request.ContentKeys {
			contentId := p.toContentId(contentKey)
			if contentId == nil {
				return nil, nil, ErrNilContentKey
			}
			if !inRange(p.Self().ID(), p.Radius(), contentId) {
				continue
			}
			if _, err := p.storage.Get(contentKey, contentId); err != nil {
				contentKeyBitlist.SetBitAt(uint64(i), true)
				acceptContentKeys = append(acceptContentKeys, contentKey)
			}
		}
	}
	accept := &Accept{
		ContentKeys: contentKeyBitlist,
	}
	return accept, acceptContentKeys, nil
}

func (p *PortalProtocol) filterContentKeysV1(request *Offer) (CommonAccept, [][]byte, error) {
	acceptV1 := &AcceptV1{
		ContentKeys: make([]uint8, len(request.ContentKeys)),
	}
	acceptContentKeys := make([][]byte, 0)
	for i, contentKey := range request.ContentKeys {
		contentId := p.toContentId(contentKey)
		if contentId == nil {
			return nil, nil, ErrNilContentKey
		}
		if !inRange(p.Self().ID(), p.Radius(), contentId) {
			acceptV1.ContentKeys[i] = uint8(NotWithinRadius)
			continue
		}
		_, err := p.storage.Get(contentKey, contentId)
		if err == nil {
			acceptV1.ContentKeys[i] = uint8(AlreadyStored)
			continue
		}
		if exist := p.transferringKeyCache.Has(contentKey); exist {
			acceptV1.ContentKeys[i] = uint8(InboundTransferInProgress)
			continue
		}
		acceptV1.ContentKeys[i] = uint8(Accepted)
		acceptContentKeys = append(acceptContentKeys, contentKey)
	}
	return acceptV1, acceptContentKeys, nil
}

func (p *PortalProtocol) cacheTransferringKeys(contentKeys [][]byte) {
	for _, key := range contentKeys {
		p.transferringKeyCache.Set(key, EmptyBytes)
	}
}

func (p *PortalProtocol) deleteTransferringContentKeys(contentKeys [][]byte) {
	for _, key := range contentKeys {
		p.transferringKeyCache.Del(key)
	}
}

func (p *PortalProtocol) parseOfferResp(node *enode.Node, data []byte) (CommonAccept, error) {
	version, err := p.getOrStoreHighestVersion(node)
	if err != nil {
		return nil, err
	}
	switch version {
	case 0:
		accept := &Accept{}
		err = accept.UnmarshalSSZ(data)
		if err != nil {
			return nil, err
		}
		return accept, nil
	case 1:
		accept := &AcceptV1{}
		err = accept.UnmarshalSSZ(data)
		if err != nil {
			return nil, err
		}
		return accept, nil
	default:
		return nil, ErrUnsupportedVersion
	}
}

// findTheBiggestSameNumber finds the largest value that exists in both slices.
// Returns the largest common value, or an error if there are no common values.
func findBiggestSameNumber(a []uint8, b []uint8) (uint8, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, errors.New("empty slice provided")
	}

	// Create a map to track values in the first slice
	valuesInA := make(map[uint8]bool)
	for _, val := range a {
		valuesInA[val] = true
	}

	// Find common values and track the maximum
	var maxCommon uint8
	foundCommon := false

	for _, val := range b {
		if valuesInA[val] {
			foundCommon = true
			if val > maxCommon {
				maxCommon = val
			}
		}
	}

	if !foundCommon {
		return 0, errors.New("no common values found")
	}

	return maxCommon, nil
}

func (p *PortalProtocol) handleV0Offer(data []byte) []byte {
	// if currentVersions includes version 1, then we need to handle the offer
	if slices.Contains(p.currentVersions, 1) {
		bitlist := bitfield.Bitlist(data)
		v1 := make([]byte, 0)
		for i := 0; i < int(bitlist.Len()); i++ {
			exist := bitlist.BitAt(uint64(i))
			if exist {
				v1 = append(v1, byte(Accepted))
			} else {
				v1 = append(v1, byte(GenericDeclined))
			}
		}
		return v1
	} else {
		return data
	}
}

func (p *PortalProtocol) decodeUtpContent(target *enode.Node, data []byte) ([]byte, error) {
	version, err := p.getOrStoreHighestVersion(target)
	if err != nil {
		return nil, err
	}
	if version == 1 {
		content, remaining, err := decodeSingleContent(data)
		if err != nil {
			return nil, err
		}

		if len(remaining) > 0 {
			return nil, errors.New("content length mismatch")
		}

		return content, nil
	}
	return data, nil
}

func (p *PortalProtocol) encodeUtpContent(target *enode.Node, data []byte) ([]byte, error) {
	version, err := p.getOrStoreHighestVersion(target)
	if err != nil {
		return nil, err
	}
	if version == 1 {
		return encodeSingleContent(data), nil
	}
	return data, nil
}
