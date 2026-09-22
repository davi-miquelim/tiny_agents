package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/davi-miquelim/tiny_agents/agent"
	"github.com/davi-miquelim/tiny_agents/model"
	"github.com/davi-miquelim/tiny_agents/tool"
)

type addArgs struct {
	A int `json:"a" desc:"First number"`
	B int `json:"b" desc:"Second number"`
}

func addNumbers(_ context.Context, args addArgs) (any, error) {
	fmt.Printf("tool add_numbers(%d, %d)\n", args.A, args.B)
	return map[string]any{"sum": args.A + args.B}, nil
}

func main() {
	add, err := tool.CreateTool("Add two integers and return the sum", addNumbers)
	if err != nil {
		log.Fatal(err)
	}

	params, err := model.NewParameters(model.OpenRouter)
	if err != nil {
		log.Fatal(err)
	}
	params.Model = "moonshotai/kimi-k2.6"
	a := agent.Agent{
		Role:  "Calculator",
		Goals: []string{"Use the add tool, then tell the user the result"},
		Instructions: "You must call " + add.Function.Name +
			" with a=2 and b=3. Then reply with the sum in one sentence.",
		Provider:    model.OpenRouter,
		ModelParams: params,
		Tools:       []tool.CallableTool{add},
	}

	buff := &agent.MessagesBuffer{}
	if err := agent.AppendUserMessage(buff, "Add 2 and 3 using the add tool."); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := agent.Complete(ctx, a, buff); err != nil {
		log.Fatal(err)
	}
	fmt.Println(buff.Messages[len(buff.Messages)-1].Content)
}
