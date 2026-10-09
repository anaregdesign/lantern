package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

// Called after leaving the committed graph view. Original receipt provenance
// and the whole-batch blind/detailed projection are frozen independently of
// current disclosure. No receipt status or result is reinterpreted on renewal.
func bindCurrentReceiptOutput(ctx context.Context, a *security.Admission, req proto.Message, observations []mutationreceipt.Observation, blind bool) error {
	if a == nil {
		return nil
	}
	if _, current := a.CurrentProfile(); !current {
		return nil
	}
	resources, err := currentReceiptOutputResources(observations, blind)
	if err != nil {
		return err
	}
	return bindCurrentDataOutput(ctx, a, req, resources)
}

func currentReceiptOutputResources(observations []mutationreceipt.Observation, blind bool) ([]security.PublicOutputResource, error) {
	if len(observations) > 1<<18 {
		return nil, dataPermissionError()
	}
	resources := make([]security.PublicOutputResource, 0, len(observations))
	for _, observation := range observations {
		r := observation.Receipt
		actions := []security.Action{security.ReceiptRead}
		if observation.Status != mutationreceipt.Confirmed {
			actions = append(actions, security.VertexRead, security.EdgeRead)
		} else {
			switch r.Kind {
			case mutationreceipt.PutVertex:
				actions = append(actions, security.VertexRead, security.VertexWrite)
				if r.LifecycleReduction || r.Resource == (mutationreceipt.ResourceIdentity{}) {
					actions = append(actions, security.VertexDelete)
				}
			case mutationreceipt.DeleteVertex:
				actions = append(actions, security.VertexRead, security.VertexDelete)
			case mutationreceipt.CreateEdge:
				actions = append(actions, security.EdgeCreate)
			case mutationreceipt.AddEdge:
				actions = append(actions, security.EdgeAdd)
			case mutationreceipt.PutEdge:
				actions = append(actions, security.EdgeWrite)
			case mutationreceipt.DeleteEdge, mutationreceipt.DeleteEdgeContribution:
				actions = append(actions, security.EdgeDelete)
			default:
				return nil, dataPermissionError()
			}
			if !blind && r.Resource.Head != "" {
				actions = append(actions, security.EdgeRead)
			}
		}
		// Core owns bounded, copied canonical receipt rows. Include the exact
		// original result bytes as well as intent, group, resource and index.
		raw, err := json.Marshal(observation)
		if err != nil {
			return nil, err
		}
		resources = append(resources, security.PublicOutputResource{Kind: security.OutputReceipt, Key: r.Resource.Key, Head: r.Resource.Head, Actions: actions, Provenance: sha256.Sum256(raw)})
	}
	return resources, nil
}
