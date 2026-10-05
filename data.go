package aviato

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

// DataClient reads and writes records through the agent's Data API, under the caller's
// permissions and audit trail. Plugin code must use it instead of querying the database
// directly. It authenticates with the invocation token of the call in progress, so it is only
// valid while that call is being handled.
type DataClient struct {
	client pluginv1connect.DataServiceClient
	token  string
}

// NewDataClient returns a Data API client for the call made by caller. Handlers receive one
// already; this is for code that only has a [Caller].
func NewDataClient(caller Caller, httpClient *http.Client) *DataClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &DataClient{
		client: pluginv1connect.NewDataServiceClient(httpClient, caller.DataURL, connect.WithProtoJSON()),
		token:  caller.InvocationToken,
	}
}

func authorized[T any](c *DataClient, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+c.token)
	return request
}

// ListOptions narrows a [DataClient.List] call.
type ListOptions struct {
	// Filter in MongoDB query syntax.
	Filter map[string]any
	// Sort is a field name, prefixed with - for a descending sort.
	Sort string
	// Limit defaults to 50.
	Limit  uint32
	Offset uint32
	// Fields to return; all readable fields when empty.
	Fields []string
}

// ListResult is a page of records and the total number of matching records.
type ListResult struct {
	Records []Record
	Total   uint64
}

// List returns a page of records of collection.
func (c *DataClient) List(ctx context.Context, collection string, options ListOptions) (ListResult, error) {
	filter, err := toStruct(options.Filter)
	if err != nil {
		return ListResult{}, err
	}
	limit := options.Limit
	if limit == 0 {
		limit = 50
	}
	response, err := c.client.List(ctx, authorized(c, &pluginv1.ListRequest{
		Collection: collection,
		Filter:     filter,
		Sort:       options.Sort,
		Limit:      limit,
		Offset:     options.Offset,
		Fields:     options.Fields,
	}))
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Records: fromStructs(response.Msg.GetRecords()), Total: response.Msg.GetTotal()}, nil
}

// Get returns one record, or nil when the agent returned none.
func (c *DataClient) Get(ctx context.Context, collection, id string) (Record, error) {
	response, err := c.client.Get(ctx, authorized(c, &pluginv1.GetRequest{Collection: collection, Id: id}))
	if err != nil {
		return nil, err
	}
	return fromStruct(response.Msg.GetRecord()), nil
}

// Create creates a record and returns it as stored.
func (c *DataClient) Create(ctx context.Context, collection string, values Record) (Record, error) {
	payload, err := toStruct(values)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Create(ctx, authorized(c, &pluginv1.CreateRequest{Collection: collection, Values: payload}))
	if err != nil {
		return nil, err
	}
	return fromStruct(response.Msg.GetRecord()), nil
}

// Update updates a record and returns it as stored.
func (c *DataClient) Update(ctx context.Context, collection, id string, values Record) (Record, error) {
	payload, err := toStruct(values)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Update(ctx, authorized(c, &pluginv1.UpdateRequest{Collection: collection, Id: id, Values: payload}))
	if err != nil {
		return nil, err
	}
	return fromStruct(response.Msg.GetRecord()), nil
}

// Delete deletes a record.
func (c *DataClient) Delete(ctx context.Context, collection, id string) error {
	_, err := c.client.Delete(ctx, authorized(c, &pluginv1.DeleteRequest{Collection: collection, Id: id}))
	return err
}
