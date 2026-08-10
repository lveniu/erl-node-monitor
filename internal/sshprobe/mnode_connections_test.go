package sshprobe

import (
	"reflect"
	"testing"
)

func TestParseMNodeIConsoleClassifiesCentralRegionAndGameConnections(t *testing.T) {
	output := `-----------------------------connected   node --------------------------
nodeid----stat-------node-----------------------statepid-------mymsgpid------czone-
703000004 2   wl_act_4@127.0.0.1                <9190.1235.0>  <9190.1722.0>  0
801000001 2   wl_ssjj_1@172.19.33.98            <9190.365.0>   ***************0
901100005 1   wl_ssjj_100005@172.19.33.104      <9352.355.0>   <9352.504.0>   0

------------------------------ process name ---------------------------`

	got, err := parseMNodeIConsole(output)
	if err != nil {
		t.Fatal(err)
	}
	want := []MNodeConnection{
		{NodeID: "703000004", State: 2, Node: "wl_act_4@127.0.0.1", Type: "game", Usable: true},
		{NodeID: "801000001", State: 2, Node: "wl_ssjj_1@172.19.33.98", Type: "central", Usable: true},
		{NodeID: "901100005", State: 1, Node: "wl_ssjj_100005@172.19.33.104", Type: "region", Usable: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connections = %#v, want %#v", got, want)
	}
}

func TestParseMNodeIConsoleAllowsNoConnections(t *testing.T) {
	output := `nodeid----stat-------node-----------------------statepid-------mymsgpid------czone-

------------------------------ process name ---------------------------`
	got, err := parseMNodeIConsole(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("connections = %#v, want none", got)
	}
}
