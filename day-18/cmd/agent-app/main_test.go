package main

import "testing"

func TestParseMCPServers(t *testing.T) {
	connector, err := parseMCPServers("github=http://github:8083/mcp,currency=http://currency:8085/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if len(connector.Targets) != 2 || connector.Targets[0].Name != "github" || connector.Targets[1].Name != "currency" {
		t.Fatalf("targets = %#v", connector.Targets)
	}
}

func TestParseMCPServersRejectsInvalidAndDuplicateEntries(t *testing.T) {
	for _, value := range []string{"", "http://localhost/mcp", "github=ftp://localhost/mcp", "github=http://one/mcp,github=http://two/mcp"} {
		if _, err := parseMCPServers(value); err == nil {
			t.Errorf("%q must fail", value)
		}
	}
}
