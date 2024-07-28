package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/machinebox/graphql"
)

var query = `
	query($userName:String!, $id:ID) { 
	  user(login: $userName){
	    repositoriesContributedTo(includeUserRepositories: true, contributionTypes: COMMIT, first: 100) {
	      pageInfo {
	        hasNextPage
	        endCursor
	      }
	      nodes {
	        defaultBranchRef {
	          target {
	            ... on Commit {
	              history(author: {id: $id}) {
	                pageInfo {
	                  hasNextPage
	                  endCursor
	                }
	                nodes {
	                  commitUrl
	                  author {
	                    email
	                    name
	                  }
	                }
	              }
	            }
	          }
	        }
	      }
	    }
	  }
	}`

func main() {
	username := flag.String("user", "", "(REQUIRED) Username of the target github account")
	printsource := flag.Bool("source", false, "Print commit URLs alongside discovered identities")
	showall := flag.Bool("all", false, "Print all commits (will repeat duplicate identities)")
	flagtoken := flag.String("token", "", "Github API Bearer token (can also be set from the GH_TOKEN env variable)")

	flag.Parse()

	if *username == "" {
		flag.PrintDefaults()
		os.Exit(0)
	}

	var token = ""

	if *flagtoken == "" {
		token = os.Getenv("GH_TOKEN")
	} else {
		token = *flagtoken
	}

	if token == "" {
		fmt.Println("Github token missing. Please generate one and set it through the -token flag or the GH_TOKEN environment variable")
		os.Exit(0)
	}

	userreq, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/users/%s", *username), nil)
	if err != nil {
		panic(err)
	}

	userreq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	httpclient := http.Client{}
	res, err := httpclient.Do(userreq)
	if err != nil {
		fmt.Printf("Error fetching user ID: %s\n", err)
		os.Exit(1)
	}

	if res.StatusCode == 401 {
		fmt.Println("Your Github token seems to be invalid.")
		os.Exit(1)
	}

	var userres struct {
		NodeID string `json:"node_id"`
	}

	err = json.NewDecoder(res.Body).Decode(&userres)
	if err != nil {
		fmt.Printf("Error parsing github api response: %s\n", err)
		os.Exit(1)
	}

	userid := userres.NodeID

	client := graphql.NewClient("https://api.github.com/graphql")

	req := graphql.NewRequest(query)

	req.Var("userName", username)
	req.Var("id", userid)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	var respData struct {
		User struct {
			RepositoriesContributedTo struct {
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
				Nodes []struct {
					DefaultBranchRef struct {
						Target struct {
							History struct {
								PageInfo struct {
									HasNextPage bool
									EndCursor   string
								}
								Nodes []struct {
									CommitURL string
									Author    struct {
										Email string
										Name  string
									}
								}
							}
						}
					}
				}
			}
		}
	}

	err = client.Run(context.Background(), req, &respData)
	if err != nil {
		log.Fatalf("Failed to execute request: %v", err)
	}

	unique := make(map[string]bool)

	for _, repo := range respData.User.RepositoriesContributedTo.Nodes {
		for _, commit := range repo.DefaultBranchRef.Target.History.Nodes {
			identity := fmt.Sprintf("%s <%s>", commit.Author.Name, commit.Author.Email)
			if _, exists := unique[identity]; *showall || !exists {
				unique[identity] = true
				if *printsource {
					fmt.Printf("%s - %s\n", identity, commit.CommitURL)
				} else {
					fmt.Println(identity)
				}
			}
		}
	}
}
