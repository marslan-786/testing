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
	NumberApiURL = BaseURL + "/ints/agent/res/data_smsnumberstats.php" // Stats API
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
	Username   string // Dynamic Username
	Password   string // Dynamic Password
}

// NewClient now accepts username and password
func NewClient(username, password string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		HTTPClient: &http.Client{
			Jar:     jar,
			Timeout: 60 * time.Second,
		},
		Username: username,
		Password: password,
	}
}

func (c *Client) ensureSession() error {
	if c.SessKey != "" {
		return nil
	}
	return c.performLogin()
}

func (c *Client) performLogin() error {
	fmt.Println("[D-Group] >> Step 1: Login Page")
	req, _ := http.NewRequest("GET", LoginURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K)")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyString := string(bodyBytes)

	// Captcha: 7 + 4 = ?
	re := regexp.MustCompile(`What is (\d+) \+ (\d+) = \?`)
	matches := re.FindStringSubmatch(bodyString)
	if len(matches) < 3 {
		return errors.New("captcha math failed")
	}
	n1, _ := strconv.Atoi(matches[1])
	n2, _ := strconv.Atoi(matches[2])
	captchaAns := strconv.Itoa(n1 + n2)
	fmt.Printf("[D-Group] Captcha Solved: %s\n", captchaAns)

	// Login Post using Dynamic Credentials
	data := url.Values{}
	data.Set("username", c.Username) // Uses the username passed in NewClient
	data.Set("password", c.Password) // Uses the password passed in NewClient
	data.Set("capt", captchaAns)

	loginReq, _ := http.NewRequest("POST", SigninURL, bytes.NewBufferString(data.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K)")
	resp, err = c.HTTPClient.Do(loginReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Get SessKey
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
		fmt.Println("[D-Group] Found SessKey:", c.SessKey)
	} else {
		return errors.New("sesskey not found or login failed")
	}

	return nil
}

// ---------------------- SMS LOGIC (Removing User) ----------------------

func (c *Client) GetSMSLogs() ([]byte, error) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()

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

		if bytes.Contains(body, []byte("<!DOCTYPE html>")) {
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
			msg = strings.ReplaceAll(msg, "null", "") // Clean explicit null text

			newRow := []interface{}{
				row[0], // Date
				row[1], // Range
				row[2], // Number
				row[3], // Service
				// Skipped Index 4 (User)
				msg,    // Message (Moved Up)
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

// ---------------------- NUMBERS LOGIC (Adding Country & Prefix) ----------------------

func (c *Client) GetNumberStats() ([]byte, error) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()

	for i := 0; i < 2; i++ {
		if err := c.ensureSession(); err != nil {
			return nil, err
		}

		now := time.Now()
		params := url.Values{}
		params.Set("fdate1", now.Format("2006-01-02")+" 00:00:00")
		params.Set("fdate2", now.Format("2006-01-02")+" 23:59:59")
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
		// D-Group Raw: [Number(0), Count(1), Currency(2), Price(3), Status(4)]
		// Target: [CountryName, Prefix, Number, Count, Currency, Price, Status]
		if len(row) > 0 {
			fullNumStr, ok := row[0].(string)
			if !ok {
				continue
			}

			// Detect Country & Prefix using Google's Lib
			countryName := "Unknown"
			countryPrefix := ""

			// Add '+' if missing for parsing
			parseNumStr := fullNumStr
			if !strings.HasPrefix(parseNumStr, "+") {
				parseNumStr = "+" + parseNumStr
			}

			numObj, err := phonenumbers.Parse(parseNumStr, "")
			if err == nil {
				// Get Country Code (Prefix) e.g. 58
				countryPrefix = strconv.Itoa(int(numObj.GetCountryCode()))
				
				// Get Region Code e.g. "VE"
				regionCode := phonenumbers.GetRegionCodeForNumber(numObj)
				
				// Convert "VE" to "Venezuela"
				countryName = getCountryName(regionCode)
			}

			// New Row Construction
			newRow := []interface{}{
				countryName,   // 0: Country Name (New)
				countryPrefix, // 1: Country Code (New)
				fullNumStr,    // 2: Full Number
				row[1],        // 3: Count
				row[2],        // 4: Currency
				row[3],        // 5: Price
				row[4],        // 6: Status
			}
			processedRows = append(processedRows, newRow)
		}
	}

	apiResp.AAData = processedRows
	// Update total records count just in case
	apiResp.ITotalRecords = len(processedRows)
	apiResp.ITotalDisplayRecords = len(processedRows)

	return json.Marshal(apiResp)
}

// Helper to map Region Codes (ISO 2 char) to Full Names
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
	return code // Return code (e.g. VE) if full name not found
}
