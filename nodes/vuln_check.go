package nodes

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"regexp"
	"time"
)

const url = "https://geth.ethereum.org/docs/vulnerabilities/vulnerabilities.json"

var (
	checkCache      []vulnJson
	lastCheckUpdate time.Time
	// for testing
	disableVulnCheck bool
)

type vulnJson struct {
	Name        string
	Uid         string
	Summary     string
	Description string
	Links       []string
	Introduced  string
	Fixed       string
	Published   string
	Severity    string
	Check       string
	CVE         string

	regex *regexp.Regexp `json:"-"`
}

func fetchChecks(url string) ([]vulnJson, error) {
	if disableVulnCheck {
		return nil, nil
	}
	client := http.Client{
		Timeout: time.Second * 1, // Timeout after 1 seconds
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return []vulnJson{}, err
	}
	req.Header.Set("User-Agent", "nodemonitor")

	res, err := client.Do(req)
	if err != nil {
		return []vulnJson{}, err
	}

	defer res.Body.Close()

	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return []vulnJson{}, err
	}

	var vulns []vulnJson
	if err = json.Unmarshal(data, &vulns); err != nil {
		return []vulnJson{}, err
	}

	checks := make([]vulnJson, 0, len(vulns))
	for _, vuln := range vulns {
		r, err := regexp.Compile(vuln.Check)
		if err != nil {
			return []vulnJson{}, err
		}
		vuln.regex = r
		checks = append(checks, vuln)
	}
	return checks, err
}

func checkNode(node Node) ([]vulnJson, error) {
	// Update the check cache every 10 minutes
	var v []vulnJson
	if checkCache != nil || time.Since(lastCheckUpdate) > 10*time.Minute {
		checks, err := fetchChecks(url)
		if err != nil {
			return v, err
		}
		checkCache = checks
	}

	version, err := node.Version()
	if err != nil {
		return v, err
	}
	for _, c := range checkCache {
		if c.regex.MatchString(version) {
			v = append(v, c)
		}
	}
	return v, nil
}
