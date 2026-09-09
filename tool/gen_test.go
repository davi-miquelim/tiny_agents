package tool

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

type GetWeatherArgs struct {
	Location string   `json:"location" desc:"City name"`
	Unit     string   `json:"unit,omitempty" enum:"celsius,fahrenheit" default:"celsius"`
	Days     int      `json:"days" description:"Forecast length"`
	Tags     []string `json:"tags" desc:"Optional labels" required:"false"`
	Meta     map[string]string
	hidden   string
}

type WeatherResult struct {
	Summary string  `json:"summary"`
	TempC   float64 `json:"temp_c"`
}

func GetWeather(_ context.Context, args GetWeatherArgs) (any, error) {
	unit := args.Unit
	if unit == "" {
		unit = "celsius"
	}
	return WeatherResult{
		Summary: args.Location + " (" + unit + ")",
		TempC:   18.5,
	}, nil
}

func TestCreateToolAndCallIntegration(t *testing.T) {
	ct, err := CreateTool("Get current weather for a location", GetWeather)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	if ct.Type != "function" {
		t.Errorf("Type = %q, want function", ct.Type)
	}
	fn := ct.Function
	if fn.Name != "get_weather" {
		t.Errorf("Name = %q, want get_weather", fn.Name)
	}
	if fn.Description != "Get current weather for a location" {
		t.Errorf("Description = %q", fn.Description)
	}
	params := fn.Parameters
	if params.Type != "object" {
		t.Errorf("Parameters.Type = %q, want object", params.Type)
	}

	loc, ok := params.Properties["location"]
	if !ok {
		t.Fatal("missing location property")
	}
	if loc.Type != "string" || loc.Description != "City name" {
		t.Errorf("location = %+v", loc)
	}

	unit, ok := params.Properties["unit"]
	if !ok {
		t.Fatal("missing unit property")
	}
	if unit.Type != "string" || unit.Default != "celsius" {
		t.Errorf("unit = %+v", unit)
	}
	if !slices.Equal(unit.Enum, []string{"celsius", "fahrenheit"}) {
		t.Errorf("unit.Enum = %v", unit.Enum)
	}

	days, ok := params.Properties["days"]
	if !ok {
		t.Fatal("missing days property")
	}
	if days.Type != "integer" || days.Description != "Forecast length" {
		t.Errorf("days = %+v", days)
	}

	tags, ok := params.Properties["tags"]
	if !ok {
		t.Fatal("missing tags property")
	}
	if tags.Type != "array" {
		t.Errorf("tags.Type = %q, want array", tags.Type)
	}
	if tags.Items == nil || tags.Items.Type != "string" {
		t.Errorf("tags.Items = %+v, want string items", tags.Items)
	}

	meta, ok := params.Properties["meta"]
	if !ok {
		t.Fatal("missing meta property (snake_cased field name)")
	}
	if meta.Type != "object" {
		t.Errorf("meta.Type = %q, want object", meta.Type)
	}

	if _, ok := params.Properties["hidden"]; ok {
		t.Error("unexported field should not appear in properties")
	}

	wantRequired := []string{"location", "days"}
	for _, name := range wantRequired {
		if !slices.Contains(params.Required, name) {
			t.Errorf("Required missing %q; got %v", name, params.Required)
		}
	}
	for _, name := range []string{"unit", "tags", "meta"} {
		if slices.Contains(params.Required, name) {
			t.Errorf("Required unexpectedly contains %q; got %v", name, params.Required)
		}
	}

	// Simulate a model ToolCall: JSON arguments → Call → typed result.
	toolCall := ToolCall{
		Id:   "call_weather_1",
		Type: "function",
		Function: FunctionCall{
			Name:      fn.Name,
			Arguments: `{"location":"Minas Tirith","unit":"celsius","days":3,"tags":["gondor"]}`,
		},
	}
	if toolCall.Function.Name != "get_weather" {
		t.Fatalf("tool call name = %q", toolCall.Function.Name)
	}

	out, err := ct.Call(context.Background(), toolCall.Function.Arguments)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	result, ok := out.(WeatherResult)
	if !ok {
		t.Fatalf("result type = %T, want WeatherResult", out)
	}
	if result.Summary != "Minas Tirith (celsius)" || result.TempC != 18.5 {
		t.Errorf("result = %+v", result)
	}

	content, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	toolResult := ToolCallResult{
		Role:       "tool",
		ToolCallId: toolCall.Id,
		Content:    string(content),
	}
	if toolResult.ToolCallId != "call_weather_1" {
		t.Errorf("ToolCallId = %q", toolResult.ToolCallId)
	}
	if !strings.Contains(toolResult.Content, "Minas Tirith") {
		t.Errorf("Content = %q", toolResult.Content)
	}
}

func TestCreateToolCallInvalidJSON(t *testing.T) {
	ct, err := CreateTool("Get weather", GetWeather)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	_, err = ct.Call(context.Background(), `{not-json`)
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
	if !strings.Contains(err.Error(), "unmarshal tool args") {
		t.Errorf("error = %v", err)
	}
}

func TestCreateToolErrorsOnNonStruct(t *testing.T) {
	_, err := CreateTool("echo", func(_ context.Context, s string) (any, error) { return s, nil })
	if err == nil {
		t.Fatal("expected error for non-struct T")
	}
	if !strings.Contains(err.Error(), "must be a struct") {
		t.Errorf("error = %v", err)
	}
}

type LookupBookArgs struct {
	Title  string `json:"title"`
	Author string `json:"-"`
}

func LookupBook(_ context.Context, args LookupBookArgs) (any, error) {
	return args.Title, nil
}

func TestCreateToolSkipsJSONDashFields(t *testing.T) {
	ct, err := CreateTool("Look up a book", LookupBook)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	props := ct.Function.Parameters.Properties
	if _, ok := props["author"]; ok {
		t.Error("json:\"-\" field should be skipped")
	}
	if _, ok := props["title"]; !ok {
		t.Fatal("missing title")
	}

	out, err := ct.Call(context.Background(), `{"title":"The Silmarillion","author":"ignored"}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "The Silmarillion" {
		t.Errorf("out = %v", out)
	}
}

type UpsertRequest struct {
	Name  string  `json:"name"`
	Email *string `json:"email"`
}

type schemaGapArgs struct {
	ID int `json:"id"`
	time.Time
	UpsertRequest
	Extra UpsertRequest `json:"upsert_request"`
	When  time.Time     `json:"when"`
	Until *time.Time    `json:"until"`
	IDs   []int         `json:"ids"`
}

func schemaGap(_ context.Context, args schemaGapArgs) (any, error) {
	return args.ID, nil
}

type ptrEmbedArgs struct {
	ID int `json:"id"`
	*UpsertRequest
}

func ptrEmbed(_ context.Context, args ptrEmbedArgs) (any, error) {
	return args.ID, nil
}

func TestCreateToolSchemaGaps(t *testing.T) {
	ct, err := CreateTool("Schema gaps", schemaGap)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	props := ct.Function.Parameters.Properties
	req := ct.Function.Parameters.Required

	for _, name := range []string{"id", "name", "email", "time"} {
		if _, ok := props[name]; !ok {
			t.Errorf("missing flattened %q; got %d properties", name, len(props))
		}
	}
	if props["email"].Type != "string" {
		t.Errorf("email.Type = %q, want string", props["email"].Type)
	}
	nested, ok := props["upsert_request"]
	if !ok || nested.Type != "object" {
		t.Errorf("named nested struct = %+v, want object", nested)
	}
	if props["when"].Type != "string" || props["until"].Type != "string" || props["time"].Type != "string" {
		t.Errorf("time types when=%q until=%q time=%q", props["when"].Type, props["until"].Type, props["time"].Type)
	}

	for _, name := range []string{"id", "name", "when", "time", "upsert_request"} {
		if !slices.Contains(req, name) {
			t.Errorf("Required missing %q; got %v", name, req)
		}
	}
	for _, name := range []string{"email", "until", "ids"} {
		if slices.Contains(req, name) {
			t.Errorf("Required unexpectedly contains %q; got %v", name, req)
		}
	}

	ptr, err := CreateTool("Pointer embed", ptrEmbed)
	if err != nil {
		t.Fatalf("CreateTool ptr: %v", err)
	}
	if _, ok := ptr.Function.Parameters.Properties["name"]; !ok {
		t.Fatal("missing flattened name from *UpsertRequest")
	}
}

func TestMapToJSONStringIntegration(t *testing.T) {
	s, err := MapToJSONString(map[string]any{"name": "get_weather", "ok": true})
	if err != nil {
		t.Fatalf("MapToJSONString: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(s), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["name"] != "get_weather" || got["ok"] != true {
		t.Errorf("got = %#v", got)
	}
}
