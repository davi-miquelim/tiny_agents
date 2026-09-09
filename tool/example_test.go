package tool_test

import (
	"context"
	"fmt"

	"github.com/steckerfy/tiny_agents/tool"
)

type addArgs struct {
	A int `json:"a" desc:"First number"`
	B int `json:"b" desc:"Second number"`
}

func addNumbers(_ context.Context, args addArgs) (any, error) {
	return map[string]any{"sum": args.A + args.B}, nil
}

func ExampleCreateTool() {
	ct, err := tool.CreateTool("Add two integers", addNumbers)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(ct.Function.Name)
	fmt.Println(ct.Function.Parameters.Properties["a"].Type)
	// Output:
	// add_numbers
	// integer
}
