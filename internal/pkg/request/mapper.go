package request

import (
	"fmt"
	"github.com/Foundato/kelon/configs"
	"github.com/pkg/errors"
	"log"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

type PathAmbiguousError struct {
	RequestUrl string
	FirstMatch string
	OtherMatch string
}

type PathNotFoundError struct {
	RequestUrl string
}

type MapperOutput struct {
	Datastore string
	Package   string
}

func (e *PathAmbiguousError) Error() string {
	return fmt.Sprintf("Path-mapping [%s] is ambiguous! Mapping [%s] also matches incoming path [%s]!", e.RequestUrl, e.FirstMatch, e.OtherMatch)
}

func (e *PathNotFoundError) Error() string {
	return fmt.Sprintf("PathMapper: There is no mapping which matches path [%s]!", e.RequestUrl)
}

type PathMapper interface {
	Configure(appConf *configs.AppConfig) error
	Map(interface{}) (*MapperOutput, error)
}

type pathMapperInput struct {
	Method string
	Url    *url.URL
}

type pathMapper struct {
	appConf    *configs.AppConfig
	mappings   []*compiledMapping
	configured bool
}

type compiledMapping struct {
	matcher    *regexp.Regexp
	mapping    *configs.ApiMapping
	importance int
	datastore  string
}

func NewPathMapper() PathMapper {
	return &pathMapper{
		appConf:    nil,
		configured: false,
	}
}

func (mapper *pathMapper) Configure(appConf *configs.AppConfig) error {
	if appConf == nil {
		return errors.New("PathMapper: AppConfig not configured! ")
	}
	mapper.appConf = appConf
	if err := mapper.generateMappings(); err != nil {
		return errors.Wrap(err, "PathMapper: Error while parsing config")
	}
	mapper.configured = true
	log.Println("Configured PathMapper")
	return nil
}

func (mapper pathMapper) Map(input interface{}) (*MapperOutput, error) {
	if !mapper.configured {
		return nil, errors.New("PathMapper was not configured! Please call Configure(). ")
	}

	// Check type and handle request
	switch in := input.(type) {
	case *pathMapperInput:
		if in.Url == nil {
			return nil, errors.New("PathMapper: Argument URL mustn't be nil! ")
		}
		if len(in.Method) == 0 {
			return nil, errors.New("PathMapper: Argument Method mustn't be empty! ")
		}
		return mapper.handleInput(in)
	default:
		return nil, errors.New("PathMapper: Input of Process() was not of type *request.pathMapperInput! Type was: " + reflect.TypeOf(input).String())
	}
}

func (mapper pathMapper) handleInput(input *pathMapperInput) (*MapperOutput, error) {
	var matches []*compiledMapping
	request := fmt.Sprintf("%s-%s", input.Method, input.Url.Path)
	for _, mapping := range mapper.mappings {
		if mapping.matcher.MatchString(request) {
			matches = append(matches, mapping)
		}
	}

	// Sort by importance descending
	if len(matches) > 0 {
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].importance > matches[j].importance
		})

		// Throw error if ambiguous paths are matched
		if len(matches) > 1 && matches[0].importance == matches[1].importance {
			return nil, &PathAmbiguousError{
				RequestUrl: request,
				FirstMatch: matches[0].mapping.Path,
				OtherMatch: matches[1].mapping.Path,
			}
		}

		if mapper.appConf.Debug {
			log.Printf("Found matching API-Mapping [%s]", matches[0].matcher.String())
		}

		// Match found
		return &MapperOutput{
			Datastore: matches[0].datastore,
			Package:   matches[0].mapping.Package,
		}, nil
	} else {
		// No matches at all
		return nil, &PathNotFoundError{
			RequestUrl: request,
		}
	}
}

func (mapper *pathMapper) generateMappings() error {
	for _, dsMapping := range mapper.appConf.Api.Mappings {

		pathPrefix := dsMapping.Prefix
		for _, mapping := range dsMapping.Mappings {

			endpointsRegex := "[(GET)|(POST)|(PUT)|(DELETE)|(PATCH)]"
			endpointsCount := 0
			if mapping.Methods != nil && len(mapping.Methods) > 0 {
				endpointsCount = len(mapping.Methods)
				anchoredMappings := make([]string, endpointsCount)
				for i, method := range mapping.Methods {
					anchoredMappings[i] = fmt.Sprintf("(%s)", method)
				}
				endpointsRegex = strings.ToUpper(fmt.Sprintf("[%s]", strings.Join(anchoredMappings, "|")))
			}

			queriesRegex := ""
			queriesCount := 0
			if mapping.Queries != nil && len(mapping.Queries) > 0 {
				queriesRegex = fmt.Sprintf("?%s=.*?", strings.Join(mapping.Queries, "=.*?"))
				queriesCount = len(mapping.Queries)
			}

			regex, err := regexp.Compile(fmt.Sprintf("%s-%s%s%s", endpointsRegex, pathPrefix, mapping.Path, queriesRegex))
			if err != nil {
				return errors.Wrap(err, "PathMapper: Error during parsing config")
			}

			mapper.mappings = append(mapper.mappings, &compiledMapping{
				matcher:    regex,
				mapping:    mapping,
				importance: len(pathPrefix) + len(mapping.Path) + queriesCount + endpointsCount,
				datastore:  dsMapping.Datastore,
			})
		}
	}
	return nil
}
