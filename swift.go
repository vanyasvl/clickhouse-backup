package main

import (
	"context"
	"fmt"

	"github.com/ncw/swift/v2"
)

func main() {
	c := swift.Connection{
		UserName: "41870",
		AuthUrl:  "https://auth.files.lu01.cloud.servers.com:5000/v3/",
		ApiKey:   "lbvhCTVoGNqSYHgA",
		Domain:   "default", // Name of the domain (v3 auth only)
		Tenant:   "1952",    // Name of the tenant (v2 auth only)
	}
	// Authenticate
	ctx := context.Background()
	err := c.Authenticate(ctx)
	if err != nil {
		panic(err)
	}
	// List all the containers
	containers, err := c.ContainerNames(ctx, nil)
	fmt.Println(containers)
	info, _, err := c.Object(ctx, "test", "20N_NIDEC.pdf")
	fmt.Println(info.Bytes)
}
