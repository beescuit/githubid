package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/machinebox/graphql"
)

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

	var token string

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

	unique := make(map[string]bool)

	var repoCursor *string = nil
	for {
		var repoQuery = `
        query ($userName: String!, $repoCursor: String) {
          user(login: $userName) {
            repositoriesContributedTo(
              includeUserRepositories: true
              contributionTypes: [COMMIT]
              first: 50
              after: $repoCursor
            ) {
              pageInfo {
                hasNextPage
                endCursor
              }
              nodes {
                name
                owner {
                    login
                }
              }
            }
          }
        }`

		req := graphql.NewRequest(repoQuery)
		req.Var("userName", *username)
		if repoCursor != nil {
			req.Var("repoCursor", *repoCursor)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

		var repoResp struct {
			User struct {
				RepositoriesContributedTo struct {
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
					Nodes []struct {
						Name  string
						Owner struct {
							Login string
						}
					}
				}
			}
		}

		err = client.Run(context.Background(), req, &repoResp)
		if err != nil {
			log.Fatalf("Failed to execute request: %v", err)
		}

		for _, repo := range repoResp.User.RepositoriesContributedTo.Nodes {
			repoName := repo.Name
			ownerLogin := repo.Owner.Login

			var refCursor *string = nil
			for {
				var refQuery = `
                query ($owner: String!, $name: String!, $refCursor: String) {
                  repository(owner: $owner, name: $name) {
                    refs(first: 10, refPrefix: "refs/heads/", after: $refCursor) {
                      pageInfo {
                        hasNextPage
                        endCursor
                      }
                      nodes {
                        name
                      }
                    }
                  }
                }`
				req := graphql.NewRequest(refQuery)
				req.Var("owner", ownerLogin)
				req.Var("name", repoName)
				if refCursor != nil {
					req.Var("refCursor", *refCursor)
				}
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

				var refResp struct {
					Repository struct {
						Refs struct {
							PageInfo struct {
								HasNextPage bool
								EndCursor   string
							}
							Nodes []struct {
								Name string
							}
						}
					}
				}

				err = client.Run(context.Background(), req, &refResp)
				if err != nil {
					log.Fatalf("Failed to execute request: %v", err)
				}

				for _, ref := range refResp.Repository.Refs.Nodes {
					refName := ref.Name

					var commitCursor *string = nil
					for {
						var commitQuery = `
                        query ($owner: String!, $name: String!, $refName: String!, $authorId: ID!, $commitCursor: String) {
                          repository(owner: $owner, name: $name) {
                            ref(qualifiedName: $refName) {
                              target {
                                ... on Commit {
                                  history(author: {id: $authorId}, first: 50, after: $commitCursor) {
                                    pageInfo {
                                      hasNextPage
                                      endCursor
                                    }
                                    nodes {
                                      commitUrl
                                      author {
                                        name
                                        email
                                      }
                                    }
                                  }
                                }
                              }
                            }
                          }
                        }`
						req := graphql.NewRequest(commitQuery)
						req.Var("owner", ownerLogin)
						req.Var("name", repoName)
						req.Var("refName", refName)
						req.Var("authorId", userid)
						if commitCursor != nil {
							req.Var("commitCursor", *commitCursor)
						}
						req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

						var commitResp struct {
							Repository struct {
								Ref struct {
									Target struct {
										History struct {
											PageInfo struct {
												HasNextPage bool
												EndCursor   string
											}
											Nodes []struct {
												CommitURL string `json:"commitUrl"`
												Author    struct {
													Name  string
													Email string
												}
											}
										}
									} `json:"target"`
								}
							}
						}

						err = client.Run(context.Background(), req, &commitResp)
						if err != nil {
							log.Fatalf("Failed to execute request: %v", err)
						}

						if commitResp.Repository.Ref.Target.History.Nodes == nil {
							break
						}

						for _, commit := range commitResp.Repository.Ref.Target.History.Nodes {
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

						if !commitResp.Repository.Ref.Target.History.PageInfo.HasNextPage {
							break
						}
						commitCursor = &commitResp.Repository.Ref.Target.History.PageInfo.EndCursor
						time.Sleep(500 * time.Millisecond)
					}
				}

				if !refResp.Repository.Refs.PageInfo.HasNextPage {
					break
				}
				refCursor = &refResp.Repository.Refs.PageInfo.EndCursor
				time.Sleep(500 * time.Millisecond)
			}
		}

		if !repoResp.User.RepositoriesContributedTo.PageInfo.HasNextPage {
			break
		}
		repoCursor = &repoResp.User.RepositoriesContributedTo.PageInfo.EndCursor
		time.Sleep(500 * time.Millisecond)
	}
}
