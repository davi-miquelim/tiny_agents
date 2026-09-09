# Examples

Runnable `package main` programs. Each one calls `agent.Complete` against OpenRouter.

Set a key first:

```bash
# Unix
export OPENROUTER_API_KEY=sk-or-...

# Windows (PowerShell)
$env:OPENROUTER_API_KEY = "sk-or-..."
```

Run from the repo root:

```bash
go run ./examples/without_tools
go run ./examples/with_tools
go run ./examples/multiturn
```

All three programs set `params.Model` to `moonshotai/kimi-k2.6`. Change that string if you want a different Kimi (or any other OpenRouter model).

## without_tools

One user message, no tools. `Complete` does a single model round and appends the assistant text to `MessagesBuffer`.

What you should see: one short hello.

## with_tools

Same `Complete` loop, with an executable `add_numbers` tool. The model should call the tool; the library runs it and sends the result back; then the model replies.

What you should see: a `tool add_numbers(2, 3)` line (printed from the Go function), then a sentence with the sum `5`.

Tool calls and tool results stay in the request-local buffer. The portable `MessagesBuffer` only keeps user and assistant text.

## multiturn

Two `Complete` calls on the **same** `MessagesBuffer`. First turn stores a name and city; second turn asks for them back.

What you should see: a reply to each turn, then a four-message transcript (`user`, `assistant`, `user`, `assistant`). The second reply should mention Ada and Lisbon.

That is the whole multi-turn loop: append a user message, call `Complete`, repeat. No extra runtime.
