package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type keyResp struct {
	Key string `json:"key"`
}

type devicesResp struct {
	Devices []struct {
		NodeKey string `json:"nodeKey"`
		ID      string `json:"id"`
	} `json:"devices"`
}

func main() {
	fmt.Println("tailscale-router: setting up")
	tailnet := "-"
	api_key := os.Getenv("TAILSCALE_API_TOKEN")

	fmt.Println("tailscale-router: tailnet name", tailnet)

	tailscale_binary_path := "/app/tailscale"

	if runtime.GOOS == "darwin" {
		output, err := exec.Command("bash", "-c", "ps -xo comm | grep MacOS/Tailscale").Output()
		if err != nil {
			panic(err)
		}
		tailscale_binary_path = strings.TrimSuffix(string(output), "\n")
	}

	fmt.Println("tailscale-router: grepping /etc/hosts to get fly-local-6pn")
	output, err := exec.Command("grep", "fly-local-6pn", "/etc/hosts").Output()
	if err != nil {
		fmt.Println("ERROR: get subnet")
		panic(err)
	}

	fmt.Println("tailscale-router: calculating subnet")
	subnet := strings.Join(strings.Split(strings.TrimSuffix(string(output), "\n"), ":")[0:3], ":") + "::/48"

	// With /var/lib/tailscale on a volume, tailscaled comes back already
	// authenticated and still advertising its routes, and the control plane has
	// already approved them against the same node identity. Everything below is
	// then redundant, so skip it: the API token is only needed to register.
	if backendState(tailscale_binary_path) == "Running" && advertisedRoutes(tailscale_binary_path) == subnet {
		fmt.Println("tailscale-router: already registered and advertising", subnet)
		fmt.Println("tailscale-router: nothing to do")
		os.Exit(0)
	}

	// Not ephemeral: Tailscale reaps ephemeral nodes once they go offline, which
	// would invalidate the identity persisted on the /var/lib/tailscale volume
	// between deploys, defeating the point of keeping it.
	var jsonData = []byte(`{
		"capabilities": {
			"devices": {
				"create": {
					"reusable": true,
					"ephemeral": false
				}
			}
		}
	}`)

	fmt.Println("tailscale-router: creating auth key")
	request, err := http.NewRequest("POST", fmt.Sprintf("https://api.tailscale.com/api/v2/tailnet/%s/keys", tailnet), bytes.NewBuffer(jsonData))
	if err != nil {
		panic(err)
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.SetBasicAuth(api_key, "")

	client := &http.Client{}
	response, error := client.Do(request)
	if error != nil {
		fmt.Println("ERROR: Create key")
		panic(error)
	}
	defer response.Body.Close()

	if response.StatusCode/100 != 2 {
		fmt.Println("ERROR: Create key returned", response.Status)
		panic("tailscale rejected the auth key request; check TAILSCALE_API_TOKEN")
	}

	var out keyResp
	err = json.NewDecoder(response.Body).Decode(&out)
	if err != nil {
		fmt.Println("ERROR: Decode key resp")
		panic(error)
	}
	key := out.Key

	// An empty key here would otherwise reach `tailscale up --authkey=` and
	// silently fall back to interactive browser login, leaving the machine
	// running but unauthorized.
	if key == "" {
		panic("tailscale returned an empty auth key")
	}

	fmt.Println("tailscale-router: auth key created")

	fmt.Println("tailscale-router: running tailscale up")
	upcmd := exec.Command("bash", "-c", fmt.Sprintf("%s up --auth-key=%s --advertise-routes=%s", tailscale_binary_path, key, subnet))
	err = upcmd.Run()
	if err != nil {
		panic(err)
	}

	fmt.Println("tailscale-router: getting PublicKey from tailscale status")
	output, err = exec.Command("bash", "-c", fmt.Sprintf("%s status --json | jq -r .Self.PublicKey", tailscale_binary_path)).Output()
	if err != nil {
		panic(err)
	}

	nodeKey := strings.TrimSuffix(string(output), "\n")

	fmt.Println("tailscale-router: getting all devices")
	request, err = http.NewRequest("GET", fmt.Sprintf("https://api.tailscale.com/api/v2/tailnet/%s/devices", tailnet), nil)
	if err != nil {
		panic(err)
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.SetBasicAuth(api_key, "")

	client = &http.Client{}
	response, error = client.Do(request)
	if error != nil {
		fmt.Println("ERROR: read devices")
		panic(error)
	}
	defer response.Body.Close()

	var devicesOut devicesResp
	err = json.NewDecoder(response.Body).Decode(&devicesOut)
	if err != nil {
		fmt.Println("ERROR: Decode key resp")
		panic(error)
	}

	selfID := ""

	fmt.Println("tailscale-router: finding our ID")
	for _, v := range devicesOut.Devices {
		if v.NodeKey == nodeKey {
			selfID = v.ID
			break
		}
	}

	jsonData = []byte(fmt.Sprintf(`{
		"routes": ["%s"]
	}`, subnet))

	fmt.Println("tailscale-router: configuring routes")
	request, err = http.NewRequest("POST", fmt.Sprintf("https://api.tailscale.com/api/v2/device/%s/routes", selfID), bytes.NewBuffer(jsonData))
	if err != nil {
		panic(err)
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.SetBasicAuth(api_key, "")

	client = &http.Client{}
	response, error = client.Do(request)
	if error != nil {
		panic(error)
	}
	defer response.Body.Close()

	fmt.Println("tailscale-router: fully configured")
	os.Exit(0)
}

// backendState polls tailscaled until it reports a state it won't leave on its
// own. start.sh backgrounds tailscaled and runs this immediately, so a single
// read would race the daemon through NoState/Starting and send an already
// registered router down the re-registration path.
func backendState(tailscaleBinary string) string {
	for i := 0; i < 30; i++ {
		out, err := exec.Command("bash", "-c", fmt.Sprintf("%s status --json | jq -r .BackendState", tailscaleBinary)).Output()
		if err == nil {
			switch state := strings.TrimSpace(string(out)); state {
			case "Running", "NeedsLogin", "Stopped":
				return state
			}
		}
		time.Sleep(time.Second)
	}

	return "unknown"
}

// advertisedRoutes returns the single route this node advertises, or "" for any
// other shape. Comparing it against the subnet we just calculated means a
// volume restored into a different 6PN re-registers instead of quietly
// advertising a route that no longer exists.
func advertisedRoutes(tailscaleBinary string) string {
	out, err := exec.Command("bash", "-c", fmt.Sprintf("%s status --json | jq -r '.Self.PrimaryRoutes // [] | join(\",\")'", tailscaleBinary)).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
