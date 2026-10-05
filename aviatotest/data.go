package aviatotest

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	aviato "github.com/getaviato/aviato-go"
	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

// FakeData is an in-memory Data API served over HTTP, standing in for the agent. Records are
// identified by their "id" field. List supports equality and $in filters on top-level fields,
// a single sort field (prefixed with - for descending), limit, offset and field selection.
type FakeData struct {
	pluginv1connect.UnimplementedDataServiceHandler
	server *httptest.Server
	token  string

	mu          sync.Mutex
	collections map[string][]aviato.Record
	nextID      int
}

func newFakeData(tb testing.TB) *FakeData {
	data := &FakeData{token: "inv_" + rand.Text(), collections: map[string][]aviato.Record{}}
	path, handler := pluginv1connect.NewDataServiceHandler(data, connect.WithInterceptors(data.authenticate()))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	data.server = httptest.NewServer(mux)
	tb.Cleanup(data.server.Close)
	return data
}

// URL is the base URL of the fake Data API.
func (d *FakeData) URL() string { return d.server.URL }

// Seed adds records to collection.
func (d *FakeData) Seed(collection string, records ...aviato.Record) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, record := range records {
		d.collections[collection] = append(d.collections[collection], maps.Clone(record))
	}
}

// Records returns a copy of the records of collection.
func (d *FakeData) Records(collection string) []aviato.Record {
	d.mu.Lock()
	defer d.mu.Unlock()
	records := make([]aviato.Record, 0, len(d.collections[collection]))
	for _, record := range d.collections[collection] {
		records = append(records, maps.Clone(record))
	}
	return records
}

func (d *FakeData) authenticate() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			if request.Header().Get("Authorization") != "Bearer "+d.token {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid invocation token"))
			}
			return next(ctx, request)
		}
	}
}

func idOf(record aviato.Record) string {
	switch id := record["id"].(type) {
	case nil:
		return ""
	case string:
		return id
	case float64:
		return strconv.FormatFloat(id, 'f', -1, 64)
	default:
		return fmt.Sprint(id)
	}
}

func (d *FakeData) find(collection, id string) (int, bool) {
	for index, record := range d.collections[collection] {
		if idOf(record) == id {
			return index, true
		}
	}
	return 0, false
}

func toStruct(record aviato.Record) (*structpb.Struct, error) {
	normalized, err := normalize(record)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	result, err := structpb.NewStruct(normalized)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return result, nil
}

func matches(record aviato.Record, filter map[string]any) (bool, error) {
	for field, expected := range filter {
		if operators, ok := expected.(map[string]any); ok {
			for operator, operand := range operators {
				if operator != "$in" {
					return false, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("aviatotest: filter operator %s is not supported by the fake Data API", operator))
				}
				candidates, _ := operand.([]any)
				if !slices.ContainsFunc(candidates, func(candidate any) bool { return fmt.Sprint(candidate) == fmt.Sprint(record[field]) }) {
					return false, nil
				}
			}
			continue
		}
		if fmt.Sprint(expected) != fmt.Sprint(record[field]) {
			return false, nil
		}
	}
	return true, nil
}

// List implements the DataService.
func (d *FakeData) List(_ context.Context, request *connect.Request[pluginv1.ListRequest]) (*connect.Response[pluginv1.ListResponse], error) {
	message := request.Msg
	d.mu.Lock()
	defer d.mu.Unlock()
	var selected []aviato.Record
	for _, record := range d.collections[message.GetCollection()] {
		ok, err := matches(record, message.GetFilter().AsMap())
		if err != nil {
			return nil, err
		}
		if ok {
			selected = append(selected, record)
		}
	}
	if sort := message.GetSort(); sort != "" {
		field, descending := strings.TrimPrefix(sort, "-"), strings.HasPrefix(sort, "-")
		slices.SortStableFunc(selected, func(a, b aviato.Record) int {
			order := cmp.Compare(fmt.Sprint(a[field]), fmt.Sprint(b[field]))
			if descending {
				return -order
			}
			return order
		})
	}
	total := len(selected)
	offset := min(int(message.GetOffset()), total)
	end := total
	if message.GetLimit() > 0 {
		end = min(offset+int(message.GetLimit()), total)
	}
	response := &pluginv1.ListResponse{Total: uint64(total)}
	for _, record := range selected[offset:end] {
		projected := record
		if fields := message.GetFields(); len(fields) > 0 {
			projected = aviato.Record{}
			for _, field := range fields {
				if value, ok := record[field]; ok {
					projected[field] = value
				}
			}
		}
		converted, err := toStruct(projected)
		if err != nil {
			return nil, err
		}
		response.Records = append(response.Records, converted)
	}
	return connect.NewResponse(response), nil
}

// Get implements the DataService.
func (d *FakeData) Get(_ context.Context, request *connect.Request[pluginv1.GetRequest]) (*connect.Response[pluginv1.GetResponse], error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	index, ok := d.find(request.Msg.GetCollection(), request.Msg.GetId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no %s record %s", request.Msg.GetCollection(), request.Msg.GetId()))
	}
	record, err := toStruct(d.collections[request.Msg.GetCollection()][index])
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.GetResponse{Record: record}), nil
}

// Create implements the DataService.
func (d *FakeData) Create(_ context.Context, request *connect.Request[pluginv1.CreateRequest]) (*connect.Response[pluginv1.CreateResponse], error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record := request.Msg.GetValues().AsMap()
	if idOf(record) == "" {
		d.nextID++
		record["id"] = "fake_" + strconv.Itoa(d.nextID)
	}
	collection := request.Msg.GetCollection()
	d.collections[collection] = append(d.collections[collection], record)
	converted, err := toStruct(record)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.CreateResponse{Record: converted}), nil
}

// Update implements the DataService.
func (d *FakeData) Update(_ context.Context, request *connect.Request[pluginv1.UpdateRequest]) (*connect.Response[pluginv1.UpdateResponse], error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	collection := request.Msg.GetCollection()
	index, ok := d.find(collection, request.Msg.GetId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no %s record %s", collection, request.Msg.GetId()))
	}
	record := d.collections[collection][index]
	maps.Copy(record, request.Msg.GetValues().AsMap())
	converted, err := toStruct(record)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pluginv1.UpdateResponse{Record: converted}), nil
}

// Delete implements the DataService.
func (d *FakeData) Delete(_ context.Context, request *connect.Request[pluginv1.DeleteRequest]) (*connect.Response[pluginv1.DeleteResponse], error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	collection := request.Msg.GetCollection()
	index, ok := d.find(collection, request.Msg.GetId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no %s record %s", collection, request.Msg.GetId()))
	}
	d.collections[collection] = slices.Delete(d.collections[collection], index, index+1)
	return connect.NewResponse(&pluginv1.DeleteResponse{}), nil
}
