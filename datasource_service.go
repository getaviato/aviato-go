package aviato

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

// datasourceService implements the protocol's DatasourceService on top of the dispatch methods.
type datasourceService struct {
	pluginv1connect.UnimplementedDatasourceServiceHandler
	plugin *Plugin
}

var _ pluginv1connect.DatasourceServiceHandler = (*datasourceService)(nil)

// datasourceContext builds the handler context; the caller is nil for the agent's own calls.
func datasourceContext(caller *pluginv1.Caller, collection string) *DatasourceContext {
	d := &DatasourceContext{Collection: collection}
	if caller.GetUserId() != "" || caller.GetRequestId() != "" {
		converted := callerFromProto(caller)
		d.Caller = &converted
	}
	return d
}

func recordStruct(record Record) (*structpb.Struct, error) {
	if record == nil {
		return &structpb.Struct{}, nil
	}
	converted, err := toStruct(record)
	if err != nil {
		return nil, internal(err)
	}
	return converted, nil
}

func recordStructs(records []Record) ([]*structpb.Struct, error) {
	converted := make([]*structpb.Struct, 0, len(records))
	for _, record := range records {
		value, err := recordStruct(record)
		if err != nil {
			return nil, err
		}
		converted = append(converted, value)
	}
	return converted, nil
}

func (s *datasourceService) DescribeCollections(context.Context, *connect.Request[pluginv1.DescribeCollectionsRequest]) (*connect.Response[pluginv1.DescribeCollectionsResponse], error) {
	return connect.NewResponse(s.plugin.DescribeCollections()), nil
}

func (s *datasourceService) List(ctx context.Context, request *connect.Request[pluginv1.DatasourceServiceListRequest]) (*connect.Response[pluginv1.DatasourceServiceListResponse], error) {
	message := request.Msg
	page, err := s.plugin.dispatchDatasourceList(ctx, datasourceContext(message.GetCaller(), message.GetCollection()), DatasourceQuery{
		Filter:       fromStruct(message.GetFilter()),
		Sort:         parseSort(message.GetSort()),
		Limit:        message.GetLimit(),
		Offset:       message.GetOffset(),
		Fields:       message.GetFields(),
		IncludeTotal: message.GetIncludeTotal(),
	})
	if err != nil {
		return nil, err
	}
	records, err := recordStructs(page.Records)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.DatasourceServiceListResponse{Records: records, Total: page.Total}), nil
}

func (s *datasourceService) Get(ctx context.Context, request *connect.Request[pluginv1.DatasourceServiceGetRequest]) (*connect.Response[pluginv1.DatasourceServiceGetResponse], error) {
	message := request.Msg
	record, err := s.plugin.dispatchDatasourceGet(ctx, datasourceContext(message.GetCaller(), message.GetCollection()), message.GetId())
	if err != nil {
		return nil, err
	}
	converted, err := recordStruct(record)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.DatasourceServiceGetResponse{Record: converted}), nil
}

func (s *datasourceService) Create(ctx context.Context, request *connect.Request[pluginv1.DatasourceServiceCreateRequest]) (*connect.Response[pluginv1.DatasourceServiceCreateResponse], error) {
	message := request.Msg
	record, err := s.plugin.dispatchDatasourceCreate(ctx, datasourceContext(message.GetCaller(), message.GetCollection()), fromStruct(message.GetValues()))
	if err != nil {
		return nil, err
	}
	converted, err := recordStruct(record)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.DatasourceServiceCreateResponse{Record: converted}), nil
}

func (s *datasourceService) Update(ctx context.Context, request *connect.Request[pluginv1.DatasourceServiceUpdateRequest]) (*connect.Response[pluginv1.DatasourceServiceUpdateResponse], error) {
	message := request.Msg
	record, err := s.plugin.dispatchDatasourceUpdate(ctx, datasourceContext(message.GetCaller(), message.GetCollection()), message.GetId(), fromStruct(message.GetValues()))
	if err != nil {
		return nil, err
	}
	converted, err := recordStruct(record)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.DatasourceServiceUpdateResponse{Record: converted}), nil
}

func (s *datasourceService) Delete(ctx context.Context, request *connect.Request[pluginv1.DatasourceServiceDeleteRequest]) (*connect.Response[pluginv1.DatasourceServiceDeleteResponse], error) {
	message := request.Msg
	if err := s.plugin.dispatchDatasourceDelete(ctx, datasourceContext(message.GetCaller(), message.GetCollection()), message.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.DatasourceServiceDeleteResponse{}), nil
}

func (s *datasourceService) ListChanges(ctx context.Context, request *connect.Request[pluginv1.ListChangesRequest]) (*connect.Response[pluginv1.ListChangesResponse], error) {
	message := request.Msg
	changes, err := s.plugin.dispatchListChanges(ctx, message.GetCollection(), message.GetCursor())
	if err != nil {
		return nil, err
	}
	records, err := recordStructs(changes.Records)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.ListChangesResponse{Records: records, DeletedIds: changes.DeletedIDs, NextCursor: changes.NextCursor, HasMore: changes.HasMore}), nil
}
