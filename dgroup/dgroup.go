package dgroup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	// Google's libphonenumber library for Go
	"github.com/nyaruka/phonenumbers"
)

// URLs
const (
	BaseURL      = "http://139.99.63.204"
	LoginURL     = BaseURL + "/ints/login"
	SigninURL    = BaseURL + "/ints/signin"
	ReportsPage  = BaseURL + "/ints/agent/SMSCDRReports"
	SMSApiURL    = BaseURL + "/ints/agent/res/data_smscdr.php"
	NumberApiURL = BaseURL + "/ints/agent/res/data_smsnumberstats.php"
)

// Wrapper for JSON Response
type ApiResponse struct {
	SEcho                interface{}     `json:"sEcho"`
	ITotalRecords        interface{}     `json:"iTotalRecords"`
	ITotalDisplayRecords interface{}     `json:"iTotalDisplayRecords"`
	AAData               [][]interface{} `json:"aaData"`
}

type Client struct {
	HTTPClient *http.Client
	SessKey    string
	Mutex      sync.Mutex
	Username   string
	Password   string
}

// ---------------------------------------------------------
// GLOBAL SESSION STORE (To avoid re-login on every request)
// ---------------------------------------------------------
var (
	sessionStore = make(map[string]*Client)
	storeMutex   sync.Mutex
)

// GetSession: Checks if user is already logged in, returns existing client or creates new one.
func GetSession(username, password string) *Client {
	storeMutex.Lock()
	defer storeMutex.Unlock()

	// 1. Check if client exists in memory
	if client, exists := sessionStore[username]; exists {
		// Update password if changed (just in case)
		client.Password = password
		return client
	}

	// 2. Create new client if not exists
	jar, _ := cookiejar.New(nil)
	newClient := &Client{
		HTTPClient: &http.Client{
			Jar:     jar,
			Timeout: 60 * time.Second,
		},
		Username: username,
		Password: password,
	}

	// Save to store
	sessionStore[username] = newClient
	return newClient
}

// ---------------------------------------------------------
// LOGIN & SESSION MANAGEMENT
// ---------------------------------------------------------

func (c *Client) ensureSession() error {
	// If SessKey exists, skip login
	if c.SessKey != "" {
		return nil
	}
	return c.performLogin()
}

func (c *Client) performLogin() error {
	fmt.Printf("[D-Group] >> Performing FRESH Login for User: %s\n", c.Username)

	req, _ := http.NewRequest("GET", LoginURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K)")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyString := string(bodyBytes)

	// Captcha Logic
	re := regexp.MustCompile(`What is (\d+) \+ (\d+) = \?`)
	matches := re.FindStringSubmatch(bodyString)
	if len(matches) < 3 {
		return errors.New("captcha math failed")
	}
	n1, _ := strconv.Atoi(matches[1])
	n2, _ := strconv.Atoi(matches[2])
	captchaAns := strconv.Itoa(n1 + n2)
	fmt.Printf("[D-Group] Captcha Solved: %s\n", captchaAns)

	// Login Post
	data := url.Values{}
	data.Set("username", c.Username)
	data.Set("password", c.Password)
	data.Set("capt", captchaAns)

	loginReq, _ := http.NewRequest("POST", SigninURL, bytes.NewBufferString(data.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K)")
	resp, err = c.HTTPClient.Do(loginReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Extract SessKey
	reportReq, _ := http.NewRequest("GET", ReportsPage, nil)
	reportReq.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K)")
	resp, err = c.HTTPClient.Do(reportReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rBody, _ := io.ReadAll(resp.Body)

	sessRe := regexp.MustCompile(`sesskey=([a-zA-Z0-9%=]+)`)
	sessMatch := sessRe.FindStringSubmatch(string(rBody))

	if len(sessMatch) > 1 {
		c.SessKey = sessMatch[1]
		fmt.Println("[D-Group] Login Success! SessKey Saved:", c.SessKey)
	} else {
		return errors.New("sesskey not found / login failed")
	}

	return nil
}

// ---------------------------------------------------------
// SMS LOGIC (User Removed)
// ---------------------------------------------------------

func (c *Client) GetSMSLogs() ([]byte, error) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()

	// Retry loop: If session expired, it logs in again automatically
	for i := 0; i < 2; i++ {
		if err := c.ensureSession(); err != nil {
			return nil, err
		}

		now := time.Now()
		// Start Date: 1st of Current Month
		startDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

		params := url.Values{}
		params.Set("fdate1", startDate.Format("2006-01-02")+" 00:00:00")
		params.Set("fdate2", now.Format("2006-01-02")+" 23:59:59")
		params.Set("sesskey", c.SessKey)
		params.Set("sEcho", "3")
		params.Set("iDisplayLength", "100")
		params.Set("iSortingCols", "1")
		params.Set("sSortDir_0", "desc")

		req, _ := http.NewRequest("GET", SMSApiURL+"?"+params.Encode(), nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K)")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		// Check session expiry
		if bytes.Contains(body, []byte("<!DOCTYPE html>")) {
			fmt.Println("[D-Group] Session Expired. Clearing Key & Retrying...")
			c.SessKey = ""
			c.HTTPClient.Jar, _ = cookiejar.New(nil)
			continue
		}

		return cleanDGroupSMS(body)
	}
	return nil, errors.New("failed after retry")
}

func cleanDGroupSMS(rawJSON []byte) ([]byte, error) {
	var apiResp ApiResponse
	if err := json.Unmarshal(rawJSON, &apiResp); err != nil {
		return rawJSON, nil
	}

	var cleanedRows [][]interface{}

	// D-Group Raw: [Date, Range, Number, Service, User(4), Message(5), Currency, Cost, Status]
	for _, row := range apiResp.AAData {
		if len(row) > 5 {
			msg, _ := row[5].(string)
			msg = html.UnescapeString(msg)
			msg = strings.ReplaceAll(msg, "null", "")

			newRow := []interface{}{
				row[0], // Date
				row[1], // Range
				row[2], // Number
				row[3], // Service
				// Skipped Index 4 (User)
				msg,    // Message
				row[6], // Currency
				row[7], // Cost
				row[8], // Status
			}
			cleanedRows = append(cleanedRows, newRow)
		}
	}
	apiResp.AAData = cleanedRows
	return json.Marshal(apiResp)
}

// ---------------------------------------------------------
// NUMBERS LOGIC (With Country Detection)
// ---------------------------------------------------------

func (c *Client) GetNumberStats() ([]byte, error) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()

	for i := 0; i < 2; i++ {
		// Uses the same saved session
		if err := c.ensureSession(); err != nil {
			return nil, err
		}

		now := time.Now()
		params := url.Values{}
		params.Set("fdate1", now.Format("2006-01-02")+" 00:00:00")
		params.Set("fdate2", now.Format("2006-12-02")+" 23:59:59")
		params.Set("sEcho", "2")
		params.Set("iDisplayLength", "-1")

		req, _ := http.NewRequest("GET", NumberApiURL+"?"+params.Encode(), nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K)")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if bytes.Contains(body, []byte("<!DOCTYPE html>")) {
			fmt.Println("[D-Group] Session Expired on Numbers. Retrying...")
			c.SessKey = ""
			c.HTTPClient.Jar, _ = cookiejar.New(nil)
			continue
		}

		return processNumbersWithCountry(body)
	}
	return nil, errors.New("failed after retry")
}

func processNumbersWithCountry(rawJSON []byte) ([]byte, error) {
	var apiResp ApiResponse
	if err := json.Unmarshal(rawJSON, &apiResp); err != nil {
		return rawJSON, nil
	}

	var processedRows [][]interface{}

	for _, row := range apiResp.AAData {
		if len(row) > 0 {
			fullNumStr, ok := row[0].(string)
			if !ok {
				continue
			}

			// Detect Country & Prefix using Google's Lib
			countryName := "Unknown"
			countryPrefix := ""

			parseNumStr := fullNumStr
			if !strings.HasPrefix(parseNumStr, "+") {
				parseNumStr = "+" + parseNumStr
			}

			numObj, err := phonenumbers.Parse(parseNumStr, "")
			if err == nil {
				countryPrefix = strconv.Itoa(int(numObj.GetCountryCode()))
				regionCode := phonenumbers.GetRegionCodeForNumber(numObj)
				countryName = getCountryName(regionCode)
			}

			newRow := []interface{}{
				countryName,   // 0
				countryPrefix, // 1
				fullNumStr,    // 2
				row[1],        // 3
				row[2],        // 4
				row[3],        // 5
				row[4],        // 6
			}
			processedRows = append(processedRows, newRow)
		}
	}

	apiResp.AAData = processedRows
	apiResp.ITotalRecords = len(processedRows)
	apiResp.ITotalDisplayRecords = len(processedRows)

	return json.Marshal(apiResp)
}

// ---------------------------------------------------------
// FULL COUNTRY LIST (As Requested)
// ---------------------------------------------------------
func getCountryName(code string) string {
	code = strings.ToUpper(code)
	countries := map[string]string{
		"AF": "Afghanistan", "AL": "Albania", "DZ": "Algeria", "AO": "Angola", "AR": "Argentina",
		"AM": "Armenia", "AU": "Australia", "AT": "Austria", "AZ": "Azerbaijan", "BH": "Bahrain",
		"BD": "Bangladesh", "BY": "Belarus", "BE": "Belgium", "BO": "Bolivia", "BA": "Bosnia",
		"BR": "Brazil", "BG": "Bulgaria", "KH": "Cambodia", "CM": "Cameroon", "CA": "Canada",
		"CL": "Chile", "CN": "China", "CO": "Colombia", "HR": "Croatia", "CY": "Cyprus",
		"CZ": "Czech Republic", "DK": "Denmark", "EG": "Egypt", "EE": "Estonia", "ET": "Ethiopia",
		"FI": "Finland", "FR": "France", "GE": "Georgia", "DE": "Germany", "GH": "Ghana",
		"GR": "Greece", "HK": "Hong Kong", "HU": "Hungary", "IN": "India", "ID": "Indonesia",
		"IR": "Iran", "IQ": "Iraq", "IE": "Ireland", "IL": "Israel", "IT": "Italy",
		"CI": "Ivory Coast", "JM": "Jamaica", "JP": "Japan", "JO": "Jordan", "KZ": "Kazakhstan",
		"KE": "Kenya", "KW": "Kuwait", "KG": "Kyrgyzstan", "LA": "Laos", "LV": "Latvia",
		"LB": "Lebanon", "LT": "Lithuania", "MY": "Malaysia", "MX": "Mexico", "MD": "Moldova",
		"MN": "Mongolia", "MA": "Morocco", "MM": "Myanmar", "NP": "Nepal", "NL": "Netherlands",
		"NZ": "New Zealand", "NG": "Nigeria", "MK": "North Macedonia", "NO": "Norway", "OM": "Oman",
		"PK": "Pakistan", "PS": "Palestine", "PA": "Panama", "PY": "Paraguay", "PE": "Peru",
		"PH": "Philippines", "PL": "Poland", "PT": "Portugal", "QA": "Qatar", "RO": "Romania",
		"RU": "Russia", "SA": "Saudi Arabia", "RS": "Serbia", "SG": "Singapore", "SK": "Slovakia",
		"SI": "Slovenia", "ZA": "South Africa", "KR": "South Korea", "ES": "Spain", "LK": "Sri Lanka",
		"SE": "Sweden", "CH": "Switzerland", "TW": "Taiwan", "TJ": "Tajikistan", "TZ": "Tanzania",
		"TH": "Thailand", "TN": "Tunisia", "TR": "Turkey", "TM": "Turkmenistan", "UA": "Ukraine",
		"AE": "UAE", "GB": "United Kingdom", "US": "USA", "UY": "Uruguay", "UZ": "Uzbekistan",
		"VE": "Venezuela", "VN": "Vietnam", "YE": "Yemen", "ZM": "Zambia", "ZW": "Zimbabwe",
	}
	if name, ok := countries[code]; ok {
		return name
	}
	return code
}
