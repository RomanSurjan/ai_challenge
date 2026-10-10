package main

import (
	"ai-challenge/day-29/internal/experiment"
	"context"
	"encoding/json"
	"log"
	"os"
)

func main() {
	r, e := experiment.New("../day-21/artifacts/index-structural.json", "http://127.0.0.1:11434")
	if e != nil {
		log.Fatal(e)
	}
	q := "Как Decimal Parse разбирает знак и дробную часть строки и почему нельзя заменять это на float64?"
	runs := []any{}
	for n := 0; n < 3; n++ {
		p, e := r.Agent.Prepare(context.Background(), q)
		if e != nil {
			log.Fatal(e)
		}
		runs = append(runs, p.Chunks())
	}
	json.NewEncoder(os.Stdout).Encode(runs)
}
