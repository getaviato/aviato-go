package aviato

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

// service implements the protocol's PluginService on top of the dispatch methods.
type service struct {
	pluginv1connect.UnimplementedPluginServiceHandler
	plugin *Plugin
}

var _ pluginv1connect.PluginServiceHandler = (*service)(nil)

func requireCaller(caller *pluginv1.Caller) (Caller, error) {
	if caller == nil {
		return Caller{}, connect.NewError(connect.CodeInvalidArgument, errors.New("missing caller"))
	}
	return callerFromProto(caller), nil
}

func internal(err error) error {
	return connect.NewError(connect.CodeInternal, err)
}

func (s *service) actionContext(caller Caller, target *pluginv1.ActionTarget, values *structpb.Struct) *ActionContext {
	formValues := fromStruct(values)
	if formValues == nil {
		formValues = map[string]any{}
	}
	return &ActionContext{
		Invocation: s.plugin.invocation(caller, target.GetCollection()),
		RecordIDs:  target.GetRecordIds(),
		Filter:     fromStruct(target.GetFilter()),
		Segment:    target.GetSegment(),
		Search:     target.GetSearch(),
		Values:     formValues,
	}
}

func (s *service) GetManifest(context.Context, *connect.Request[pluginv1.GetManifestRequest]) (*connect.Response[pluginv1.GetManifestResponse], error) {
	manifest, err := s.plugin.Manifest()
	if err != nil {
		return nil, internal(err)
	}
	return connect.NewResponse(&pluginv1.GetManifestResponse{Manifest: manifest}), nil
}

func (s *service) ResolveForm(ctx context.Context, request *connect.Request[pluginv1.ResolveFormRequest]) (*connect.Response[pluginv1.ResolveFormResponse], error) {
	message := request.Msg
	action, err := s.plugin.lookupAction(message.GetTarget().GetCollection(), message.GetAction())
	if err != nil {
		return nil, err
	}
	var resolved FormResolution
	if action.resolveForm == nil {
		resolved = FormResolution{Form: action.form}
	} else {
		caller, err := requireCaller(message.GetCaller())
		if err != nil {
			return nil, err
		}
		form := &FormContext{ActionContext: *s.actionContext(caller, message.GetTarget(), message.GetValues()), ChangedField: message.GetChangedField()}
		if resolved, err = s.plugin.dispatchForm(ctx, message.GetAction(), form); err != nil {
			return nil, err
		}
	}
	response := &pluginv1.ResolveFormResponse{}
	if resolved.Form != nil {
		if response.FormSchema, err = toStruct(resolved.Form.Schema); err != nil {
			return nil, internal(err)
		}
		if response.FormUiSchema, err = toStruct(resolved.Form.UISchema); err != nil {
			return nil, internal(err)
		}
	}
	if response.Values, err = toStruct(resolved.Values); err != nil {
		return nil, internal(err)
	}
	return connect.NewResponse(response), nil
}

func (s *service) ExecuteAction(ctx context.Context, request *connect.Request[pluginv1.ExecuteActionRequest]) (*connect.Response[pluginv1.ExecuteActionResponse], error) {
	message := request.Msg
	if _, err := s.plugin.lookupAction(message.GetTarget().GetCollection(), message.GetAction()); err != nil {
		return nil, err
	}
	caller, err := requireCaller(message.GetCaller())
	if err != nil {
		return nil, err
	}
	result, err := s.plugin.dispatchAction(ctx, message.GetAction(), s.actionContext(caller, message.GetTarget(), message.GetValues()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.ExecuteActionResponse{Result: s.plugin.resultToProto(result)}), nil
}

func (p *Plugin) resultToProto(result Result) *pluginv1.ActionResult {
	switch result := result.(type) {
	case SuccessResult:
		return &pluginv1.ActionResult{Result: &pluginv1.ActionResult_Success{Success: &pluginv1.SuccessResult{Message: result.Message, Invalidated: result.Invalidated}}}
	case ErrorResult:
		return &pluginv1.ActionResult{Result: &pluginv1.ActionResult_Error{Error: &pluginv1.ErrorResult{Message: result.Message, Html: result.HTML}}}
	case HTMLResult:
		return &pluginv1.ActionResult{Result: &pluginv1.ActionResult_Html{Html: &pluginv1.HtmlResult{Html: result.HTML}}}
	case RedirectResult:
		return &pluginv1.ActionResult{Result: &pluginv1.ActionResult_Redirect{Redirect: &pluginv1.RedirectResult{Path: result.Path, Url: result.URL}}}
	case WebhookResult:
		method := result.Method
		if method == "" {
			method = "POST"
		}
		return &pluginv1.ActionResult{Result: &pluginv1.ActionResult_Webhook{Webhook: &pluginv1.WebhookResult{Url: result.URL, Method: method, Headers: result.Headers, Body: result.Body}}}
	case FileResult:
		ref := p.files.put(result)
		return &pluginv1.ActionResult{Result: &pluginv1.ActionResult_File{File: &pluginv1.FileResult{Ref: ref, Name: result.Name, MimeType: result.MimeType}}}
	default:
		// Result is sealed; only the types above implement it.
		return &pluginv1.ActionResult{Result: &pluginv1.ActionResult_Success{Success: &pluginv1.SuccessResult{}}}
	}
}

func (s *service) ComputeFields(ctx context.Context, request *connect.Request[pluginv1.ComputeFieldsRequest]) (*connect.Response[pluginv1.ComputeFieldsResponse], error) {
	message := request.Msg
	caller, err := requireCaller(message.GetCaller())
	if err != nil {
		return nil, err
	}
	inv := s.plugin.invocation(caller, message.GetCollection())
	values, err := s.plugin.dispatchCompute(ctx, &inv, message.GetFields(), fromStructs(message.GetRecords()))
	if err != nil {
		return nil, err
	}
	response := &pluginv1.ComputeFieldsResponse{Values: make([]*structpb.Struct, 0, len(values))}
	for _, value := range values {
		converted, err := toStruct(value)
		if err != nil {
			return nil, internal(err)
		}
		response.Values = append(response.Values, converted)
	}
	return connect.NewResponse(response), nil
}

func (s *service) WriteField(ctx context.Context, request *connect.Request[pluginv1.WriteFieldRequest]) (*connect.Response[pluginv1.WriteFieldResponse], error) {
	message := request.Msg
	caller, err := requireCaller(message.GetCaller())
	if err != nil {
		return nil, err
	}
	var value any
	if message.GetValue() != nil {
		value = message.GetValue().AsInterface()
	}
	inv := s.plugin.invocation(caller, message.GetCollection())
	patch, err := s.plugin.dispatchWrite(ctx, &inv, message.GetField(), value, fromStruct(message.GetRecord()))
	if err != nil {
		return nil, err
	}
	converted, err := toStruct(patch)
	if err != nil {
		return nil, internal(err)
	}
	return connect.NewResponse(&pluginv1.WriteFieldResponse{Patch: converted}), nil
}

func (s *service) ResolveSegment(ctx context.Context, request *connect.Request[pluginv1.ResolveSegmentRequest]) (*connect.Response[pluginv1.ResolveSegmentResponse], error) {
	message := request.Msg
	caller, err := requireCaller(message.GetCaller())
	if err != nil {
		return nil, err
	}
	inv := s.plugin.invocation(caller, message.GetCollection())
	resolved, err := s.plugin.dispatchSegment(ctx, &inv, message.GetSegment())
	if err != nil {
		return nil, err
	}
	if ids, ok := resolved.IDs(); ok {
		return connect.NewResponse(&pluginv1.ResolveSegmentResponse{Result: &pluginv1.ResolveSegmentResponse_RecordIds{RecordIds: &pluginv1.RecordIds{Ids: ids}}}), nil
	}
	filter, _ := resolved.Filter()
	converted, err := toStruct(filter)
	if err != nil {
		return nil, internal(err)
	}
	if converted == nil {
		converted = &structpb.Struct{}
	}
	return connect.NewResponse(&pluginv1.ResolveSegmentResponse{Result: &pluginv1.ResolveSegmentResponse_Filter{Filter: converted}}), nil
}

func (s *service) RunHook(ctx context.Context, request *connect.Request[pluginv1.RunHookRequest]) (*connect.Response[pluginv1.RunHookResponse], error) {
	message := request.Msg
	caller, err := requireCaller(message.GetCaller())
	if err != nil {
		return nil, err
	}
	hook := &HookContext{
		Invocation: s.plugin.invocation(caller, message.GetCollection()),
		Timing:     hookTimingFromProto(message.GetTiming()),
		Operation:  hookOperationFromProto(message.GetOperation()),
		RecordIDs:  message.GetRecordIds(),
		Values:     fromStruct(message.GetValues()),
		Action:     message.GetAction(),
	}
	outcome, err := s.plugin.dispatchHooks(ctx, hook)
	if err != nil {
		return nil, err
	}
	if outcome.Reject != "" {
		return connect.NewResponse(&pluginv1.RunHookResponse{RejectMessage: outcome.Reject}), nil
	}
	values, err := toStruct(outcome.Values)
	if err != nil {
		return nil, internal(err)
	}
	return connect.NewResponse(&pluginv1.RunHookResponse{Values: values}), nil
}

func (s *service) Search(ctx context.Context, request *connect.Request[pluginv1.SearchRequest]) (*connect.Response[pluginv1.SearchResponse], error) {
	message := request.Msg
	caller, err := requireCaller(message.GetCaller())
	if err != nil {
		return nil, err
	}
	inv := s.plugin.invocation(caller, message.GetCollection())
	filter, err := s.plugin.dispatchSearch(ctx, &inv, message.GetQuery())
	if err != nil {
		return nil, err
	}
	converted, err := toStruct(filter)
	if err != nil {
		return nil, internal(err)
	}
	return connect.NewResponse(&pluginv1.SearchResponse{Filter: converted}), nil
}

func (s *service) ComputeChart(ctx context.Context, request *connect.Request[pluginv1.ComputeChartRequest]) (*connect.Response[pluginv1.ComputeChartResponse], error) {
	message := request.Msg
	caller, err := requireCaller(message.GetCaller())
	if err != nil {
		return nil, err
	}
	spec, err := s.plugin.dispatchChart(ctx, caller, message.GetChart(), fromStruct(message.GetFilter()))
	if err != nil {
		return nil, err
	}
	converted, err := toStruct(spec)
	if err != nil {
		return nil, internal(err)
	}
	return connect.NewResponse(&pluginv1.ComputeChartResponse{VegaLite: converted}), nil
}

func hookTimingFromProto(timing pluginv1.HookTiming) HookTiming {
	switch timing {
	case pluginv1.HookTiming_HOOK_TIMING_BEFORE:
		return Before
	case pluginv1.HookTiming_HOOK_TIMING_AFTER:
		return After
	default:
		return 0
	}
}

func hookOperationFromProto(operation pluginv1.HookOperation) HookOperation {
	switch operation {
	case pluginv1.HookOperation_HOOK_OPERATION_CREATE:
		return HookCreate
	case pluginv1.HookOperation_HOOK_OPERATION_UPDATE:
		return HookUpdate
	case pluginv1.HookOperation_HOOK_OPERATION_DELETE:
		return HookDelete
	case pluginv1.HookOperation_HOOK_OPERATION_LIST:
		return HookList
	case pluginv1.HookOperation_HOOK_OPERATION_ACTION:
		return HookAction
	default:
		return 0
	}
}
