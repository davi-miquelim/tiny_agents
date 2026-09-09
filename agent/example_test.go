package agent_test

import (
	"fmt"

	"github.com/steckerfy/tiny_agents/agent"
)

func ExampleAppendUserMessage() {
	buff := &agent.MessagesBuffer{}
	if err := agent.AppendUserMessage(buff, "Hello"); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(len(buff.Messages), buff.Messages[0].Role)
	// Output:
	// 1 user
}

func ExampleNeedsCompaction() {
	buff := &agent.MessagesBuffer{Tokens: 100_000}
	fmt.Println(agent.NeedsCompaction(buff, 128_000))
	fmt.Println(agent.NeedsCompaction(buff, 80_000))
	// Output:
	// false
	// true
}

func ExampleFormatSummary() {
	text := agent.FormatSummary(agent.SummaryResult{
		Summary:       "User asked about Lisbon weather.",
		KeyPoints:     []string{"City: Lisbon", "Temp: 22C"},
		OpenQuestions: []string{"Unit preference?"},
	})
	fmt.Println(text)
	// Output:
	// Previous conversation summary:
	// User asked about Lisbon weather.
	//
	// Key points:
	// - City: Lisbon
	// - Temp: 22C
	//
	// Open questions:
	// - Unit preference?
}
