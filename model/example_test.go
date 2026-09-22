package model_test

import (
	"fmt"
	"log"

	"github.com/davi-miquelim/tiny_agents/model"
)

func ExampleNewParameters() {
	params, err := model.NewParameters(model.OpenRouter)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(params.Model)
	fmt.Println(*params.Temperature)
	fmt.Println(params.Provider["allow_fallbacks"])
	// Output:
	// openrouter/free
	// 0.7
	// true
}
