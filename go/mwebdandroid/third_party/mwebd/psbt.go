package mwebd

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/ltcutil"
	"github.com/ltcmweb/ltcd/ltcutil/mweb"
	"github.com/ltcmweb/ltcd/ltcutil/mweb/mw"
	"github.com/ltcmweb/ltcd/ltcutil/psbt"
	"github.com/ltcmweb/ltcd/txscript"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcmweb/mwebd/proto"
	"github.com/ltcmweb/mwebd/sign"
)

func (s *Server) PsbtCreate(ctx context.Context,
	req *proto.PsbtCreateRequest) (*proto.PsbtResponse, error) {

	tx := wire.NewMsgTx(2)
	if req.RawTx != nil {
		if err := tx.Deserialize(bytes.NewReader(req.RawTx)); err != nil {
			return nil, err
		}
	}

	p := &psbt.Packet{
		PsbtVersion:      2,
		TxVersion:        tx.Version,
		FallbackLocktime: &tx.LockTime,
	}
	for i, txIn := range tx.TxIn {
		txOut := req.WitnessUtxo[i]
		p.Inputs = append(p.Inputs, psbt.PInput{
			WitnessUtxo:  wire.NewTxOut(txOut.Value, txOut.PkScript),
			PrevoutHash:  &txIn.PreviousOutPoint.Hash,
			PrevoutIndex: &txIn.PreviousOutPoint.Index,
			Sequence:     &txIn.Sequence,
		})
	}
	for _, txOut := range tx.TxOut {
		p.Outputs = append(p.Outputs, psbt.POutput{
			Amount:   ltcutil.Amount(txOut.Value),
			PKScript: txOut.PkScript,
		})
	}

	b64, err := p.B64Encode()
	if err != nil {
		return nil, err
	}
	return &proto.PsbtResponse{PsbtB64: b64}, nil
}

func (s *Server) PsbtAddInput(ctx context.Context,
	req *proto.PsbtAddInputRequest) (*proto.PsbtResponse, error) {

	p, err := psbt.NewFromRawBytes(strings.NewReader(req.PsbtB64), true)
	if err != nil {
		return nil, err
	}

	outputId, err := hex.DecodeString(req.OutputId)
	if err != nil {
		return nil, err
	}

	output, err := s.fetchCoin(chainhash.Hash(outputId))
	if err != nil {
		return nil, err
	}

	coin, err := s.rewindOutput(output, (*mw.SecretKey)(req.ScanSecret))
	if err != nil {
		return nil, err
	}

	amount := ltcutil.Amount(coin.Value)

	p.Inputs = append(p.Inputs, psbt.PInput{
		MwebOutputId:          (*chainhash.Hash)(outputId),
		MwebAddressIndex:      &req.AddressIndex,
		MwebAmount:            &amount,
		MwebSharedSecret:      coin.SharedSecret,
		MwebKeyExchangePubkey: &output.Message.KeyExchangePubKey,
		MwebCommit:            &output.Commitment,
		MwebOutputPubkey:      &output.ReceiverPubKey,
	})

	s.adjustKernel(p, req.FeeRatePerKb)

	b64, err := p.B64Encode()
	if err != nil {
		return nil, err
	}
	return &proto.PsbtResponse{PsbtB64: b64}, nil
}

func (s *Server) getKernelIndex(p *psbt.Packet) (index int) {
	for _, pKernel := range p.Kernels {
		if pKernel.Signature == nil {
			break
		}
		index++
	}
	if index == len(p.Kernels) {
		pKernel := psbt.PKernel{}
		if p.FallbackLocktime != nil &&
			*p.FallbackLocktime > 0 &&
			*p.FallbackLocktime < 500_000_000 {
			lockHeight := int32(*p.FallbackLocktime)
			pKernel.LockHeight = &lockHeight
		}
		p.Kernels = append(p.Kernels, pKernel)
	}
	return
}

func (s *Server) PsbtAddRecipient(ctx context.Context,
	req *proto.PsbtAddRecipientRequest) (*proto.PsbtResponse, error) {

	p, err := psbt.NewFromRawBytes(strings.NewReader(req.PsbtB64), true)
	if err != nil {
		return nil, err
	}

	addr, err := ltcutil.DecodeAddress(req.Recipient.Address, &s.cp)
	if err != nil {
		return nil, err
	}

	kernel := &p.Kernels[s.getKernelIndex(p)]
	if mwebAddr, ok := addr.(*ltcutil.AddressMweb); ok {
		p.Outputs = append(p.Outputs, psbt.POutput{
			Amount:         ltcutil.Amount(req.Recipient.Value),
			StealthAddress: mwebAddr.StealthAddress(),
		})
	} else {
		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return nil, err
		}
		txOut := wire.NewTxOut(req.Recipient.Value, pkScript)
		kernel.PegOuts = append(kernel.PegOuts, txOut)
	}

	s.adjustKernel(p, req.FeeRatePerKb)

	b64, err := p.B64Encode()
	if err != nil {
		return nil, err
	}
	return &proto.PsbtResponse{PsbtB64: b64}, nil
}

func (s *Server) calcFee(p *psbt.Packet, feeRatePerKb uint64) uint64 {
	divCeil := func(x, y uint64) uint64 { return (x + y - 1) / y }
	var weight, txOutSize uint64
	for _, pOutput := range p.Outputs {
		if pOutput.StealthAddress != nil || pOutput.OutputCommit != nil {
			weight += mweb.StandardOutputWeight
		}
	}
	for _, pKernel := range p.Kernels {
		weight += mweb.KernelWithStealthWeight
		for _, pegout := range pKernel.PegOuts {
			weight += divCeil(uint64(len(pegout.PkScript)), mweb.BytesPerWeight)
			txOutSize += uint64(pegout.SerializeSize())
		}
	}
	return weight*mweb.BaseMwebFee + divCeil(txOutSize*feeRatePerKb, 1000)
}

func (s *Server) adjustKernel(p *psbt.Packet, feeRatePerKb uint64) {
	var (
		inputs, outputs ltcutil.Amount

		kernel = &p.Kernels[s.getKernelIndex(p)]
		fee    = ltcutil.Amount(s.calcFee(p, feeRatePerKb))
	)
	for _, pInput := range p.Inputs {
		if pInput.MwebAmount != nil {
			inputs += *pInput.MwebAmount
		}
	}
	for _, pOutput := range p.Outputs {
		if pOutput.StealthAddress != nil || pOutput.OutputCommit != nil {
			outputs += pOutput.Amount
		}
	}
	for i, pKernel := range p.Kernels {
		if pKernel.Signature != nil {
			if pKernel.Fee != nil {
				outputs += *pKernel.Fee
				fee = max(fee-*pKernel.Fee, 0)
			}
			if pKernel.PeginAmount != nil {
				inputs += *pKernel.PeginAmount
			}
		} else {
			p.Kernels[i].Fee = nil
			p.Kernels[i].PeginAmount = nil
		}
		for _, pegout := range pKernel.PegOuts {
			outputs += ltcutil.Amount(pegout.Value)
		}
	}
	if inputs < outputs+fee {
		pegin := outputs + fee - inputs
		kernel.PeginAmount = &pegin
	} else {
		fee = inputs - outputs
	}
	kernel.Fee = &fee
}

func (s *Server) PsbtGetRecipients(ctx context.Context,
	req *proto.PsbtGetRecipientsRequest) (*proto.PsbtGetRecipientsResponse, error) {

	resp, err := sign.PsbtGetRecipients(&sign.Psbt{PsbtB64: req.PsbtB64}, &s.cp)
	if err != nil {
		return nil, err
	}
	var rs []*proto.PsbtRecipient
	for _, r := range resp.Recipient {
		rs = append(rs, &proto.PsbtRecipient{
			Address: r.Address,
			Value:   r.Value,
		})
	}
	return &proto.PsbtGetRecipientsResponse{
		Recipient:    rs,
		InputAddress: resp.InputAddress,
		Fee:          resp.Fee,
	}, nil
}

func (s *Server) PsbtSign(ctx context.Context,
	req *proto.PsbtSignRequest) (*proto.PsbtResponse, error) {

	resp, err := sign.PsbtSign(&sign.PsbtSignRequest{
		PsbtB64: req.PsbtB64,
		Scan:    req.ScanSecret,
		Spend:   req.SpendSecret,
	})
	if err != nil {
		return nil, err
	}
	return &proto.PsbtResponse{PsbtB64: resp.PsbtB64}, nil
}

func (s *Server) PsbtSignNonMweb(ctx context.Context,
	req *proto.PsbtSignNonMwebRequest) (*proto.PsbtResponse, error) {

	resp, err := sign.PsbtSignPubKeyHash(&sign.PsbtSignPubKeyHashRequest{
		PsbtB64: req.PsbtB64,
		PrivKey: req.PrivKey,
		Index:   req.Index,
	})
	if err != nil {
		return nil, err
	}
	return &proto.PsbtResponse{PsbtB64: resp.PsbtB64}, nil
}

func (s *Server) PsbtExtract(ctx context.Context,
	req *proto.PsbtExtractRequest) (*proto.CreateResponse, error) {

	p, err := psbt.NewFromRawBytes(strings.NewReader(req.PsbtB64), true)
	if err != nil {
		return nil, err
	}

	var tx *wire.MsgTx
	if req.Unsigned {
		tx, err = psbt.ExtractUnsignedTx(p)
	} else {
		tx, err = psbt.Extract(p)
	}
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err = tx.Serialize(&buf); err != nil {
		return nil, err
	}

	outputId := map[mw.Commitment]chainhash.Hash{}
	if tx.Mweb != nil {
		for _, output := range tx.Mweb.TxBody.Outputs {
			outputId[output.Commitment] = *output.Hash()
		}
	}

	resp := &proto.CreateResponse{RawTx: buf.Bytes()}
	for _, pOutput := range p.Outputs {
		if pOutput.OutputCommit != nil {
			outputId := outputId[*pOutput.OutputCommit]
			resp.OutputId = append(resp.OutputId,
				hex.EncodeToString(outputId[:]))
		}
	}

	return resp, nil
}
