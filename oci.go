package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/common/auth"
	"github.com/oracle/oci-go-sdk/v65/identity"
	"github.com/oracle/oci-go-sdk/v65/secrets"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func createConfigProvider(cmd commandLine, forSecret bool) (common.ConfigurationProvider, error) {
	configFile := cmd.configPath
	profile := "DEFAULT"

	if configFile == "" {
		configFile = defaultOCIConfigFile()
	}

	if forSecret {
		if cmd.dbSecretProfile == "" || strings.EqualFold(cmd.dbSecretProfile, "local") {
			fmt.Println("INFO: Server is configured to use Instance Principals authentication")
			return auth.InstancePrincipalConfigurationProvider()
		}

		fmt.Println("INFO: Using local private key authentication (OCI config file)")
		return common.CustomProfileConfigProvider(configFile, cmd.dbSecretProfile), nil
	}

	if cmd.profile != "" {
		profile = cmd.profile
	}

	if cmd.instancePrincipals {
		fmt.Println("INFO: Server is configured to use Instance Principals authentication")
		return auth.InstancePrincipalConfigurationProvider()
	}

	fmt.Println("INFO: Using local private key authentication (OCI config file)")
	if cmd.configPath == "" && cmd.profile == "" {
		return common.DefaultConfigProvider(), nil
	}
	return common.CustomProfileConfigProvider(configFile, profile), nil
}

func defaultOCIConfigFile() string {
	if path := os.Getenv("OCI_CONFIG_FILE"); path != "" {
		return path
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		return filepath.Join(home, ".oci", "config")
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".oci", "config")
	}
	return filepath.Join(".oci", "config")
}

func applyProxy(base *common.BaseClient, proxyValue string) {
	if strings.TrimSpace(proxyValue) == "" {
		return
	}

	if !strings.HasPrefix(proxyValue, "http://") && !strings.HasPrefix(proxyValue, "https://") {
		proxyValue = "https://" + proxyValue
	}

	proxyURL, err := url.Parse(proxyValue)
	if err != nil {
		fmt.Printf("WARN: ignoring invalid proxy %q: %v\n", proxyValue, err)
		return
	}

	base.HTTPClient = &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
}

func getSecretPassword(ctx context.Context, provider common.ConfigurationProvider, proxyValue, secretID string) (string, error) {
	fmt.Println("\nConnecting to Secret Client Service...")
	secretClient, err := secrets.NewSecretsClientWithConfigurationProvider(provider)
	if err != nil {
		return "", fmt.Errorf("create secrets client: %w", err)
	}
	applyProxy(&secretClient.BaseClient, proxyValue)
	fmt.Println("Connected.")

	resp, err := secretClient.GetSecretBundle(ctx, secrets.GetSecretBundleRequest{
		SecretId: common.String(secretID),
	})
	if err != nil {
		return "", fmt.Errorf("get secret bundle: %w", err)
	}

	content := resp.SecretBundle.SecretBundleContent
	base64Content, ok := content.(secrets.Base64SecretBundleContentDetails)
	if !ok {
		return "", fmt.Errorf("secret bundle content is %T, expected Base64SecretBundleContentDetails", content)
	}

	decoded, err := base64.StdEncoding.DecodeString(value(base64Content.Content))
	if err != nil {
		return "", fmt.Errorf("decode secret content: %w", err)
	}

	fmt.Println("Secret Retrieved.")
	return string(decoded), nil
}

func getHomeRegion(ctx context.Context, client identity.IdentityClient, tenancyID string) (string, error) {
	resp, err := client.ListRegionSubscriptions(ctx, identity.ListRegionSubscriptionsRequest{
		TenancyId: common.String(tenancyID),
	})
	if err != nil {
		return "", fmt.Errorf("list region subscriptions: %w", err)
	}

	for _, reg := range resp.Items {
		if reg.IsHomeRegion != nil && *reg.IsHomeRegion {
			return value(reg.RegionName), nil
		}
	}
	return "", errors.New("home region not found")
}

func identityReadCompartments(ctx context.Context, client identity.IdentityClient, tenancy tenancyInfo) ([]compartmentInfo, error) {
	fmt.Println("Loading Compartments...")

	all, err := listAllCompartments(ctx, client, tenancy.ID)
	if err != nil {
		return nil, err
	}

	byParent := map[string][]identity.Compartment{}
	for _, c := range all {
		byParent[value(c.CompartmentId)] = append(byParent[value(c.CompartmentId)], c)
	}

	compartments := []compartmentInfo{{
		ID:   tenancy.ID,
		Name: tenancy.Name + " (root)",
		Path: "/ " + tenancy.Name + " (root)",
	}}

	var build func(parentID, path string)
	build = func(parentID, path string) {
		nextPath := path
		if nextPath != "" {
			nextPath += " / "
		}

		for _, c := range byParent[parentID] {
			if c.LifecycleState == identity.CompartmentLifecycleStateActive {
				item := compartmentInfo{
					ID:   value(c.Id),
					Name: value(c.Name),
					Path: nextPath + value(c.Name),
				}
				compartments = append(compartments, item)
				build(item.ID, item.Path)
			}
		}
	}
	build(tenancy.ID, "")

	sort.Slice(compartments, func(i, j int) bool {
		return compartments[i].Path < compartments[j].Path
	})

	fmt.Printf("    Total %d compartments loaded.\n", len(compartments))
	return compartments, nil
}

func listAllCompartments(ctx context.Context, client identity.IdentityClient, tenancyID string) ([]identity.Compartment, error) {
	var out []identity.Compartment
	var page *string

	for {
		resp, err := client.ListCompartments(ctx, identity.ListCompartmentsRequest{
			CompartmentId:          common.String(tenancyID),
			CompartmentIdInSubtree: common.Bool(true),
			Page:                   page,
		})
		if err != nil {
			return nil, fmt.Errorf("list compartments: %w", err)
		}

		out = append(out, resp.Items...)
		if resp.OpcNextPage == nil || value(resp.OpcNextPage) == "" {
			break
		}
		page = resp.OpcNextPage
	}

	return out, nil
}
