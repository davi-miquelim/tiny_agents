package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/davi-miquelim/tiny_agents/agent"
	"github.com/davi-miquelim/tiny_agents/model"
)

func main() {
	params, err := model.NewParameters(model.OpenRouter)
	if err != nil {
		log.Fatal(err)
	}
	params.Model = "moonshotai/kimi-k2.6"
	a := agent.Agent{
		Role:         "Assistant",
		Goals:        []string{"Help the user"},
		Instructions: "Be brief.",
		Provider:     model.OpenRouter,
		ModelParams:  params,
	}

	buff := &agent.MessagesBuffer{}
	if err := agent.AppendUserMessage(buff, "Say hello in one sentence."); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := agent.Complete(ctx, a, buff); err != nil {
		log.Fatal(err)
	}
	fmt.Println(buff.Messages[len(buff.Messages)-1].Content)
}
