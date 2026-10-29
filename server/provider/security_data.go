package provider

import (
	"errors"
	"net/http"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/service"
)

func (r *SecurityRuntime) protectDataService(svc *service.LanternService) error {
	if r == nil || svc == nil {
		return errors.New("missing data/security runtime")
	}
	if r.mode == "oidc" {
		if !svc.SharesServingRuntime(r.data) || svc.DataNamespaceFormat() != keyspace.Version {
			return errors.New("data service differs from security-owned namespace runtime")
		}
		svc.WithDataAuthorization(r.now)
	}
	return nil
}

// These constructors retain caller-owned limits/observability while making
// admission and the logical-key Role boundary mandatory on protected data.
func (r *SecurityRuntime) PublicDataHTTPHandler(svc *service.LanternService, options ...connect.HandlerOption) (string, http.Handler, error) {
	if err := r.protectDataService(svc); err != nil {
		return "", nil, err
	}
	options = append(options, service.StrictJSONHandlerOption(), connect.WithInterceptors(r.PublicAuthenticationInterceptor()))
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternServiceName+"/", func(procedure string, options ...connect.HandlerOption) (string, http.Handler) {
		typed := service.NewLanternServiceConnectHandler(svc)
		if procedure == "" && r.current == nil {
			return graphv1connect.NewLanternServiceHandler(typed, options...)
		}
		return "/" + graphv1connect.LanternServiceName + "/", newCurrentDataRPCHandler(procedure, typed, options...)
	}, options...)
	return path, handler, nil
}
func (r *SecurityRuntime) BrowserDataHTTPHandler(svc *service.LanternService, options ...connect.HandlerOption) (string, http.Handler, error) {
	if err := r.protectDataService(svc); err != nil {
		return "", nil, err
	}
	options = append(options, service.StrictJSONHandlerOption())
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternServiceName+"/", func(procedure string, options ...connect.HandlerOption) (string, http.Handler) {
		typed := service.NewLanternServiceConnectHandler(svc)
		if procedure == "" && r.current == nil {
			return graphv1connect.NewLanternServiceHandler(typed, options...)
		}
		return "/" + graphv1connect.LanternServiceName + "/", newCurrentDataRPCHandler(procedure, typed, options...)
	}, options...)
	return "/browser" + path, r.BrowserRPCHandler(path, handler), nil
}

// newCurrentDataRPCHandler constructs only the selected procedure's fresh
// codecs and pools. Keep schema and caller-option order identical to the
// generated service constructor; the descriptor coverage test catches drift.
func newCurrentDataRPCHandler(procedure string, svc graphv1connect.LanternServiceHandler, options ...connect.HandlerOption) http.Handler {
	methods := pb.File_graph_v1_graph_proto.Services().ByName("LanternService").Methods()
	switch procedure {
	case graphv1connect.LanternServiceIlluminateProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceIlluminateProcedure,
			svc.Illuminate,
			connect.WithSchema(methods.ByName("Illuminate")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetVertexProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetVertexProcedure,
			svc.GetVertex,
			connect.WithSchema(methods.ByName("GetVertex")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetVerticesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetVerticesProcedure,
			svc.GetVertices,
			connect.WithSchema(methods.ByName("GetVertices")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServicePutVertexProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServicePutVertexProcedure,
			svc.PutVertex,
			connect.WithSchema(methods.ByName("PutVertex")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServicePutVerticesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServicePutVerticesProcedure,
			svc.PutVertices,
			connect.WithSchema(methods.ByName("PutVertices")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceMutateGraphProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceMutateGraphProcedure,
			svc.MutateGraph,
			connect.WithSchema(methods.ByName("MutateGraph")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceDeleteVertexProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceDeleteVertexProcedure,
			svc.DeleteVertex,
			connect.WithSchema(methods.ByName("DeleteVertex")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceDeleteVerticesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceDeleteVerticesProcedure,
			svc.DeleteVertices,
			connect.WithSchema(methods.ByName("DeleteVertices")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceScanVerticesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceScanVerticesProcedure,
			svc.ScanVertices,
			connect.WithSchema(methods.ByName("ScanVertices")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceScanVertexKeysProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceScanVertexKeysProcedure,
			svc.ScanVertexKeys,
			connect.WithSchema(methods.ByName("ScanVertexKeys")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceSearchVerticesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceSearchVerticesProcedure,
			svc.SearchVertices,
			connect.WithSchema(methods.ByName("SearchVertices")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceCountVerticesByPrefixProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceCountVerticesByPrefixProcedure,
			svc.CountVerticesByPrefix,
			connect.WithSchema(methods.ByName("CountVerticesByPrefix")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceDeleteVerticesByPrefixProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceDeleteVerticesByPrefixProcedure,
			svc.DeleteVerticesByPrefix,
			connect.WithSchema(methods.ByName("DeleteVerticesByPrefix")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceTopVerticesByDegreeProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceTopVerticesByDegreeProcedure,
			svc.TopVerticesByDegree,
			connect.WithSchema(methods.ByName("TopVerticesByDegree")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetEdgeProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetEdgeProcedure,
			svc.GetEdge,
			connect.WithSchema(methods.ByName("GetEdge")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetEdgesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetEdgesProcedure,
			svc.GetEdges,
			connect.WithSchema(methods.ByName("GetEdges")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceAddEdgeProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceAddEdgeProcedure,
			svc.AddEdge,
			connect.WithSchema(methods.ByName("AddEdge")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceAddEdgesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceAddEdgesProcedure,
			svc.AddEdges,
			connect.WithSchema(methods.ByName("AddEdges")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceCreateEdgeProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceCreateEdgeProcedure,
			svc.CreateEdge,
			connect.WithSchema(methods.ByName("CreateEdge")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceCreateEdgesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceCreateEdgesProcedure,
			svc.CreateEdges,
			connect.WithSchema(methods.ByName("CreateEdges")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServicePutEdgeProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServicePutEdgeProcedure,
			svc.PutEdge,
			connect.WithSchema(methods.ByName("PutEdge")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServicePutEdgesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServicePutEdgesProcedure,
			svc.PutEdges,
			connect.WithSchema(methods.ByName("PutEdges")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceDeleteEdgeProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceDeleteEdgeProcedure,
			svc.DeleteEdge,
			connect.WithSchema(methods.ByName("DeleteEdge")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceDeleteEdgesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceDeleteEdgesProcedure,
			svc.DeleteEdges,
			connect.WithSchema(methods.ByName("DeleteEdges")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceDeleteEdgeContributionProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceDeleteEdgeContributionProcedure,
			svc.DeleteEdgeContribution,
			connect.WithSchema(methods.ByName("DeleteEdgeContribution")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceDeleteEdgeContributionsProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceDeleteEdgeContributionsProcedure,
			svc.DeleteEdgeContributions,
			connect.WithSchema(methods.ByName("DeleteEdgeContributions")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceDeleteEdgesByPrefixProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceDeleteEdgesByPrefixProcedure,
			svc.DeleteEdgesByPrefix,
			connect.WithSchema(methods.ByName("DeleteEdgesByPrefix")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceScanEdgesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceScanEdgesProcedure,
			svc.ScanEdges,
			connect.WithSchema(methods.ByName("ScanEdges")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetServerStatusProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetServerStatusProcedure,
			svc.GetServerStatus,
			connect.WithSchema(methods.ByName("GetServerStatus")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetReplicationStatusProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetReplicationStatusProcedure,
			svc.GetReplicationStatus,
			connect.WithSchema(methods.ByName("GetReplicationStatus")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetReceiptCapabilityProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetReceiptCapabilityProcedure,
			svc.GetReceiptCapability,
			connect.WithSchema(methods.ByName("GetReceiptCapability")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetReceiptStatusProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetReceiptStatusProcedure,
			svc.GetReceiptStatus,
			connect.WithSchema(methods.ByName("GetReceiptStatus")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceGetReceiptStatusesProcedure:
		return connect.NewUnaryHandler(
			graphv1connect.LanternServiceGetReceiptStatusesProcedure,
			svc.GetReceiptStatuses,
			connect.WithSchema(methods.ByName("GetReceiptStatuses")),
			connect.WithHandlerOptions(options...),
		)
	case graphv1connect.LanternServiceBackupSnapshotProcedure:
		return connect.NewServerStreamHandler(
			graphv1connect.LanternServiceBackupSnapshotProcedure,
			svc.BackupSnapshot,
			connect.WithSchema(methods.ByName("BackupSnapshot")),
			connect.WithHandlerOptions(options...),
		)
	default:
		return http.NotFoundHandler()
	}
}
