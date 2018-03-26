package main

import (
	"encoding/json"
	"fmt"
	"log"

	graphqlhelpers "github.com/btubbs/graphql-go-helpers"
	"github.com/graphql-go/graphql"
)

type helloArgs struct {
	Name     string `arg:"name" required:"true" desc:"Your name"`
	Greeting string `arg:"greeting" desc:"How to say hello"`
}

func main() {
	// Schema
	fields := graphql.Fields{
		"hello": &graphql.Field{
			Type: graphql.String,
			Args: graphqlhelpers.ArgsConfig(helloArgs{}),
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				var args helloArgs
				err := graphqlhelpers.LoadArgs(p, &args)
				if err != nil {
					return nil, err
				}

				if args.Greeting == "" {
					args.Greeting = "Hello"
				}
				return fmt.Sprintf("%s %s", args.Greeting, args.Name), nil
			},
		},
	}
	rootQuery := graphql.ObjectConfig{Name: "RootQuery", Fields: fields}
	schemaConfig := graphql.SchemaConfig{Query: graphql.NewObject(rootQuery)}
	schema, err := graphql.NewSchema(schemaConfig)
	if err != nil {
		log.Fatalf("failed to create new schema, error: %v", err)
	}

	// Query
	query := `
	{
		hello(name: "Joe", greeting: "Goodbye")
	}
	`
	//query := `
	//{
	//hello
	//}
	//`
	params := graphql.Params{Schema: schema, RequestString: query}
	r := graphql.Do(params)
	if len(r.Errors) > 0 {
		log.Fatalf("failed to execute graphql operation, errors: %+v", r.Errors)
	}
	rJSON, _ := json.Marshal(r)
	fmt.Printf("%s \n", rJSON) // {“data”:{“hello”:”world”}}
}
