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
		Goals:        []string{"Remember facts the user states"},
		Instructions: "Be brief. Use earlier turns in this conversation.",
		Provider:     model.OpenRouter,
		ModelParams:  params,
	}

	buff := &agent.MessagesBuffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := turn(ctx, a, buff, "My name is Ada. I live in Lisbon."); err != nil {
		log.Fatal(err)
	}
	if err := turn(ctx, a, buff, "What is my name, and where do I live?"); err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- transcript ---")
	for _, m := range buff.Messages {
		fmt.Printf("%s: %v\n", m.Role, m.Content)
	}
}

func turn(ctx context.Context, a agent.Agent, buff *agent.MessagesBuffer, user string) error {
	if err := agent.AppendUserMessage(buff, user); err != nil {
		return err
	}
	if _, err := agent.Complete(ctx, a, buff); err != nil {
		return err
	}
	fmt.Printf("user: %s\n", user)
	fmt.Printf("assistant: %v\n\n", buff.Messages[len(buff.Messages)-1].Content)
	return nil
}
