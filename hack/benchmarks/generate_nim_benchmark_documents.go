package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

type (
	NimCatalogResponse struct {
		ResultTotal     int      `json:"resultTotal"`
		ResultPageTotal int      `json:"resultPageTotal"`
		Params          Params   `json:"params"`
		Results         []Result `json:"results"`
	}

	Params struct {
		OrderBy     []FieldValue `json:"orderBy"`
		QueryFields []string     `json:"queryFields"`
		ScoredSize  int          `json:"scoredSize"`
		PageSize    int          `json:"pageSize"`
		Fields      []string     `json:"fields"`
		Page        int          `json:"page"`
		Filters     []FieldValue `json:"filters"`
		Query       string       `json:"query"`
		GroupBy     string       `json:"groupBy"`
	}

	FieldValue struct {
		Field string `json:"field"`
		Value string `json:"value"`
	}

	Result struct {
		TotalCount int        `json:"totalCount"`
		GroupValue string     `json:"groupValue"`
		Resources  []Resource `json:"resources"`
	}

	Resource struct {
		OrgName         string      `json:"orgName"`
		ResourceId      string      `json:"resourceId"`
		Labels          []Label     `json:"labels"`
		SharedWithTeams []string    `json:"sharedWithTeams"`
		TeamName        string      `json:"teamName"`
		MsgTimestamp    int64       `json:"msgTimestamp"`
		DateModified    string      `json:"dateModified"`
		SharedWithOrgs  []string    `json:"sharedWithOrgs"`
		Description     string      `json:"description"`
		DateCreated     string      `json:"dateCreated"`
		WeightPopular   float32     `json:"weightPopular"`
		CreatedBy       string      `json:"createdBy"`
		DisplayName     string      `json:"displayName"`
		Name            string      `json:"name"`
		ResourceType    string      `json:"resourceType"`
		Attributes      []Attribute `json:"attributes"`
	}

	Label struct {
		Key    string   `json:"key"`
		Values []string `json:"values"`
	}

	Attribute struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
)

const (
	targetFileFmt = "%s/ngc_cat_resp__rt%d__sz%d__pg%d.json"
	targetFolder  = "./hack/benchmarks/documents"
)

func main() {
	runtimes := flag.Int("runtimes", 0, "number of runtime images in the overall responses")
	pageSize := flag.Int("size", 0, "page size, number of runtimes to include in a page")

	flag.Parse()
	if *runtimes <= 0 || *pageSize <= 0 {
		panic("Please provide number of runtimes and page size")
	}

	if err := os.MkdirAll(targetFolder, os.ModePerm); err != nil {
		if !os.IsExist(err) {
			panic(err)
		}
	}

	totalPages := 1
	if *runtimes > *pageSize {
		totalPages = *runtimes / *pageSize
		if *runtimes%*pageSize > 0 {
			totalPages++
		}
	}

	currentPage := 0
	var resources []Resource
	for r := 1; r <= *runtimes; r++ {
		modelName := fmt.Sprintf("dummy-model-%d-page-%d", r, currentPage)
		resources = append(resources, makeDummyResource(modelName, fmt.Sprintf("dummy-org/dummy-team/%s", modelName)))
		if len(resources) == *pageSize || r == *runtimes {
			b, bErr := json.Marshal(makeDummyResponse(*pageSize, totalPages, currentPage, *runtimes, resources))
			if bErr != nil {
				panic(bErr)
			}
			if err := os.WriteFile(fmt.Sprintf(
				targetFileFmt, targetFolder, *runtimes, *pageSize, currentPage), b, os.ModePerm); err != nil {
				panic(err)
			}
			resources = []Resource{}
			currentPage++
		}
	}
}

func makeDummyResponse(pageSize, totalPages, currentPage, totalRuntimes int, resources []Resource) NimCatalogResponse {
	return NimCatalogResponse{
		ResultTotal:     totalRuntimes,
		ResultPageTotal: totalPages,
		Params: Params{
			OrderBy: []FieldValue{{
				Field: "score",
				Value: "DESC",
			}},
			QueryFields: []string{"name", "displayName", "all", "publisher", "builtBy", "description"},
			ScoredSize:  1,
			PageSize:    pageSize,
			Fields: []string{
				"weight_popular", "ace_name", "date_created", "resource_type", "description", "display_name", "created_by",
				"weight_featured", "team_name", "labels", "shared_with_orgs", "date_modified", "shared_with_teams",
				"is_public", "name", "resource_id", "attributes", "org_name", "guest_access", "msg_timestamp", "status"},
			Page: currentPage,
			Filters: []FieldValue{
				{
					Field: "orgName",
					Value: "nim",
				},
				{
					Field: "resourceType",
					Value: "container",
				},
			},
			Query:   "*:*",
			GroupBy: "resourceType",
		},
		Results: []Result{
			{
				TotalCount: 1,
				GroupValue: "_scored",
				Resources:  []Resource{makeDummyResource("scored-dummy-model", "nim/fake/scored-dummy-model")},
			},
			{
				TotalCount: totalRuntimes,
				GroupValue: "CONTAINER",
				Resources:  resources,
			},
		},
	}
}

func makeDummyResource(name, resourceId string) Resource {
	return Resource{
		OrgName:    "nim",
		ResourceId: resourceId,
		Labels: []Label{{
			Key:    "some-key",
			Values: []string{"one-value", "another-value"},
		}},
		SharedWithTeams: []string{"nim/some-team"},
		TeamName:        "some-team",
		MsgTimestamp:    time.Now().Unix(),
		DateModified:    time.Now().Format(time.RFC3339),
		SharedWithOrgs:  make([]string, 0),
		Description: "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Proin viverra felis arcu, a " +
			"fringilla mi posuere non. Quisque tristique justo non nibh tincidunt euismod. Fusce placerat consectetur" +
			" tempus",
		DateCreated:   time.Now().Format(time.RFC3339),
		WeightPopular: 33.44,
		CreatedBy:     "dummy-generator",
		DisplayName:   name,
		Name:          name,
		ResourceType:  "CONTAINER",
		Attributes: []Attribute{
			{
				Key:   "latestTag",
				Value: "1.2.3",
			},
			{
				Key:   "size",
				Value: "6835134649",
			},
		},
	}
}
