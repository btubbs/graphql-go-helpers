// Package graphqlhelpers provides helper functions for reducing the boilerplate needed to define
// and load graphql-go arguments.
package graphqlhelpers

import (
	"fmt"
	"reflect"
	"runtime"
	"strconv"

	"github.com/graphql-go/graphql"
)

const (
	argTag      = "arg"
	requiredTag = "required"
	descTag     = "desc"
)

var DefaultLoaders = []struct {
	LoaderFunc interface{}
	GqlType    graphql.Output
}{
	{LoaderFunc: LoadBool, GqlType: graphql.Boolean},
	{LoaderFunc: LoadString, GqlType: graphql.String},
	{LoaderFunc: LoadInt, GqlType: graphql.Int},
	{LoaderFunc: LoadFloat, GqlType: graphql.Float},
}

var defaultLoader *ArgLoader

// New returns a ArgLoader with the default loader funcs enabled.
func New() (*ArgLoader, error) {
	ec := Empty()
	for _, l := range DefaultLoaders {
		err := ec.Register(l.LoaderFunc, l.GqlType)
		if err != nil {
			return nil, err
		}
	}
	return ec, nil
}

// Empty returns a ArgLoader without any loader funcs enabled.
func Empty() *ArgLoader {
	ec := &ArgLoader{}
	ec.loaderFuncs = map[reflect.Type]func(interface{}) (reflect.Value, error){}
	ec.gqlTypes = map[reflect.Type]graphql.Output{}
	return ec
}

// ArgLoader is a helper for reading arguments from a graphql.ResolveParams, converting them to Go
// types, and setting their values to fields on a user-provided struct.
type ArgLoader struct {
	// a map from reflect types to functions that can take an interface and return a
	// reflect value of that type.
	loaderFuncs map[reflect.Type]func(interface{}) (reflect.Value, error)

	// a map from reflect types to the graphql types that should be used for their arguments.
	gqlTypes map[reflect.Type]graphql.Output
}

// ArgsConfig takes a struct instance with appropriate struct tags on its fields and returns a map
// of argument names to graphql argument configs, for assigning to the Args field in a
// graphql.Field.  If there is an error generating the argument configs, this function will panic.
func (e *ArgLoader) ArgsConfig(i interface{}) graphql.FieldConfigArgument {
	conf, err := e.SafeArgsConfig(i)
	if err != nil {
		panic(fmt.Sprintf("could not configure arguments: %v", err))
	}
	return conf
}

func (e *ArgLoader) SafeArgsConfig(i interface{}) (graphql.FieldConfigArgument, error) {
	// we should have a struct
	var structType reflect.Type

	// accept either a struct or a pointer to a struct
	iType := reflect.TypeOf(i)
	if iType.Kind() == reflect.Ptr {
		structType = iType.Elem()
	} else {
		structType = reflect.TypeOf(i)
	}
	if structType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%v is not a struct", i)
	}

	out := graphql.FieldConfigArgument{}
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		argName, ok := field.Tag.Lookup(argTag)
		if !ok {
			// this field doesn't have our tag.  Skip.
			continue
		}
		out[argName] = &graphql.ArgumentConfig{
			Type:        e.gqlTypes[field.Type],
			Description: field.Tag.Get(descTag),
		}
	}
	return out, nil
}

// RegisterParser takes a func (string) (<anytype>, error) and registers it on the ArgLoader as
// the parser for <anytype>
func (e *ArgLoader) Register(f interface{}, gqlType graphql.Output) error {
	// alright, let's inspect this f and make sure it's a func (string) (sometype, err)
	t := reflect.TypeOf(f)
	if t.Kind() != reflect.Func {
		return fmt.Errorf("%v is not a func", f)
	}

	fname := runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
	// f should accept one argument
	if t.NumIn() != 1 {
		return fmt.Errorf(
			"loader func should accept 1 interface{} argument. %v accepts %d arguments",
			fname, t.NumIn())
	}
	// it should return two things
	if t.NumOut() != 2 {
		return fmt.Errorf(
			"loader func should return 2 arguments. %v returns %d arguments",
			fname, t.NumOut())
	}
	// the first can be any type. the second should be error
	errorInterface := reflect.TypeOf((*error)(nil)).Elem()
	if !t.Out(1).Implements(errorInterface) {
		return fmt.Errorf(
			"loader func's last return value should be error. %s's last return value is %v",
			fname, t.Out(1))
	}
	_, alreadyRegistered := e.loaderFuncs[t.Out(0)]
	if alreadyRegistered {
		return fmt.Errorf("a loader func has already been registered for the %v type.  cannot also register %s",
			t.Out(0), fname,
		)
	}

	callable := reflect.ValueOf(f)
	wrapped := func(i interface{}) (v reflect.Value, err error) {
		defer func() {
			p := recover()
			if p != nil {
				// we panicked running the inner loader func.
				err = fmt.Errorf("%s panicked: %s", fname, p)
			}
		}()
		returnvals := callable.Call([]reflect.Value{reflect.ValueOf(i)})
		if !returnvals[1].IsNil() {
			return reflect.Value{}, fmt.Errorf("%v", returnvals[1])
		}
		return returnvals[0], nil
	}
	e.loaderFuncs[t.Out(0)] = wrapped
	e.gqlTypes[t.Out(0)] = gqlType
	return nil
}

// Load loads arguments from the provided map into the provided struct.
func (e *ArgLoader) LoadArgs(p graphql.ResolveParams, c interface{}) error {
	// assert that c is a struct.
	pointerType := reflect.TypeOf(c)
	if pointerType.Kind() != reflect.Ptr {
		return fmt.Errorf("%v is not a pointer", c)
	}

	structType := pointerType.Elem()
	if structType.Kind() != reflect.Struct {
		return fmt.Errorf("%v is not a pointer to a struct", c)
	}
	structVal := reflect.ValueOf(c).Elem()

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		argKey, ok := field.Tag.Lookup(argTag)
		if !ok {
			// this field doesn't have our tag.  Skip.
			continue
		}

		interfaceVal, ok := p.Args[argKey]
		if !ok {
			// could not find the key we're looking for in map.  is it required?
			requiredVal, ok := field.Tag.Lookup(requiredTag)
			if !ok {
				// no required tag, so it's not required.
				continue
			}
			required, err := strconv.ParseBool(requiredVal)
			if err != nil {
				return fmt.Errorf("%s is not a valid 'required' tag value", requiredVal)
			}
			if required {
				return fmt.Errorf("%s is required", argKey)
			} else {
				continue
			}
		}
		loaderFunc, ok := e.loaderFuncs[field.Type]
		if !ok {
			return fmt.Errorf("no loader function found for type %v", field.Type)
		}

		toSet, err := loaderFunc(interfaceVal)
		if err != nil {
			return fmt.Errorf("cannot populate %s: %v", field.Name, err)
		}
		structVal.Field(i).Set(toSet)
	}
	return nil
}

func LoadBool(i interface{}) (bool, error) {
	b, ok := i.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a bool", i)
	}
	return b, nil
}

func LoadString(i interface{}) (string, error) {
	b, ok := i.(string)
	if !ok {
		return "", fmt.Errorf("%v is not a string", i)
	}
	return b, nil
}

func LoadInt(i interface{}) (int, error) {
	b, ok := i.(int)
	if !ok {
		return 0, fmt.Errorf("%v is not an int", i)
	}
	return b, nil
}

func LoadFloat(i interface{}) (float64, error) {
	b, ok := i.(float64)
	if !ok {
		return 0, fmt.Errorf("%v is not a float", i)
	}
	return b, nil
}

func ArgsConfig(i interface{}) graphql.FieldConfigArgument {
	return defaultLoader.ArgsConfig(i)
}

// LoadArgs loads values from the provided interface map into the provided struct.
func LoadArgs(p graphql.ResolveParams, i interface{}) error {
	return defaultLoader.LoadArgs(p, i)
}

// Register takes a func (interface{}) (<anytype>, error) and registers it on the default loader
// as the loader func for <anytype>.
func Register(f interface{}, gqlType graphql.Output) error {
	return defaultLoader.Register(f, gqlType)
}

func init() {
	// we can only fail here if one of the hardcoded default loader fund has the wrong function
	// signature.  If that does fail, fail hard.
	var err error
	defaultLoader, err = New()
	if err != nil {
		panic(fmt.Sprintf("could not init default loader: %v", err))
	}
}
