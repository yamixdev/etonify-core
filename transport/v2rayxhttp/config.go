package v2rayxhttp

import (
	crand "crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"strings"

	"github.com/gofrs/uuid/v5"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"golang.org/x/net/http2/hpack"
)

const (
	PlacementQueryInHeader = "queryInHeader"
	PlacementCookie        = "cookie"
	PlacementHeader        = "header"
	PlacementQuery         = "query"
	PlacementPath          = "path"
	PlacementBody          = "body"
	PlacementAuto          = "auto"

	PaddingMethodRepeatX  = "repeat-x"
	PaddingMethodTokenish = "tokenish"

	defaultChromeMajor       = 144
	maxXPaddingBytes         = 16 * 1024
	maxUplinkChunkBytes      = 64 * 1024
	maxSessionIDLength       = 128
	maxPostsIntervalMillis   = 5 * 1000
	defaultPacketUploadBytes = maxPacketUploadBytes
)

type xhttpConfig struct {
	host    string
	path    string
	query   string
	mode    string
	headers http.Header

	xPaddingBytesFrom int32
	xPaddingBytesTo   int32
	xPaddingObfsMode  bool
	xPaddingKey       string
	xPaddingHeader    string
	xPaddingPlacement string
	xPaddingMethod    string

	noGRPCHeader bool

	uplinkHTTPMethod    string
	uplinkDataPlacement string
	uplinkDataKey       string
	uplinkChunkSizeFrom int32
	uplinkChunkSizeTo   int32

	sessionPlacement  string
	sessionKey        string
	sessionTable      string
	sessionLengthFrom int32
	sessionLengthTo   int32
	seqPlacement      string
	seqKey            string

	scMaxEachPostBytesFrom   int32
	scMaxEachPostBytesTo     int32
	scMinPostsIntervalMsFrom int32
	scMinPostsIntervalMsTo   int32
}

func newConfig(options option.V2RayXHTTPOptions) *xhttpConfig {
	config := &xhttpConfig{
		host:              options.Host,
		mode:              strings.ToLower(strings.TrimSpace(options.Mode)),
		xPaddingObfsMode:  options.XPaddingObfsMode,
		xPaddingKey:       options.XPaddingKey,
		xPaddingHeader:    options.XPaddingHeader,
		xPaddingPlacement: options.XPaddingPlacement,
		xPaddingMethod:    options.XPaddingMethod,
		noGRPCHeader:      options.NoGRPCHeader,
	}
	if config.mode == "" {
		config.mode = "auto"
	}

	pathAndQuery := strings.SplitN(options.Path, "?", 2)
	path := pathAndQuery[0]
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	config.path = path
	if len(pathAndQuery) > 1 {
		config.query = pathAndQuery[1]
	}

	config.headers = make(http.Header)
	for key, values := range options.Headers.Build() {
		for _, value := range values {
			config.headers.Add(key, value)
		}
	}
	applyDefaultFetchHeaders(config.headers)

	config.xPaddingBytesFrom, config.xPaddingBytesTo = boundedPositiveRange(
		options.XPaddingBytes,
		100,
		1000,
		1,
		maxXPaddingBytes,
	)

	config.uplinkHTTPMethod = strings.ToUpper(strings.TrimSpace(options.UplinkHTTPMethod))
	if config.uplinkHTTPMethod == "" {
		config.uplinkHTTPMethod = http.MethodPost
	}
	config.uplinkDataPlacement = options.UplinkDataPlacement
	if config.uplinkDataPlacement == "" {
		config.uplinkDataPlacement = PlacementAuto
	}
	config.uplinkDataKey = options.UplinkDataKey
	if config.uplinkDataKey == "" && config.uplinkDataPlacement != PlacementBody {
		if config.uplinkDataPlacement == PlacementCookie {
			config.uplinkDataKey = "x_data"
		} else {
			config.uplinkDataKey = "X-Data"
		}
	}
	if options.UplinkChunkSize != nil && rangeHasPositiveValue(options.UplinkChunkSize) {
		config.uplinkChunkSizeFrom, config.uplinkChunkSizeTo = boundedPositiveRange(
			options.UplinkChunkSize,
			64,
			64,
			64,
			maxUplinkChunkBytes,
		)
	}

	config.sessionPlacement = options.SessionPlacement
	if config.sessionPlacement == "" {
		config.sessionPlacement = PlacementPath
	}
	config.seqPlacement = options.SeqPlacement
	if config.seqPlacement == "" {
		config.seqPlacement = PlacementPath
	}
	config.sessionKey = options.SessionKey
	if config.sessionKey == "" {
		switch config.sessionPlacement {
		case PlacementHeader:
			config.sessionKey = "X-Session"
		case PlacementCookie, PlacementQuery:
			config.sessionKey = "x_session"
		}
	}
	config.sessionTable = normalizeSessionTable(options.SessionTable)
	if options.SessionLength != nil && rangeHasPositiveValue(options.SessionLength) {
		config.sessionLengthFrom, config.sessionLengthTo = boundedPositiveRange(
			options.SessionLength,
			1,
			1,
			1,
			maxSessionIDLength,
		)
	}
	config.seqKey = options.SeqKey
	if config.seqKey == "" {
		switch config.seqPlacement {
		case PlacementHeader:
			config.seqKey = "X-Seq"
		case PlacementCookie, PlacementQuery:
			config.seqKey = "x_seq"
		}
	}

	config.scMaxEachPostBytesFrom, config.scMaxEachPostBytesTo = boundedPositiveRange(
		options.ScMaxEachPostBytes,
		defaultPacketUploadBytes,
		defaultPacketUploadBytes,
		64,
		maxPacketUploadBytes,
	)
	if config.uplinkChunkSizeTo == 0 {
		switch config.uplinkDataPlacement {
		case PlacementCookie:
			config.uplinkChunkSizeFrom = 2 * 1024
			config.uplinkChunkSizeTo = 3 * 1024
		case PlacementHeader:
			config.uplinkChunkSizeFrom = 3 * 1000
			config.uplinkChunkSizeTo = 4 * 1000
		default:
			config.uplinkChunkSizeFrom = config.scMaxEachPostBytesFrom
			config.uplinkChunkSizeTo = config.scMaxEachPostBytesTo
		}
	}
	config.scMinPostsIntervalMsFrom, config.scMinPostsIntervalMsTo = boundedPositiveRange(
		options.ScMinPostsIntervalMs,
		30,
		30,
		1,
		maxPostsIntervalMillis,
	)

	return config
}

func (c *xhttpConfig) validate() error {
	switch c.uplinkDataPlacement {
	case PlacementAuto, PlacementBody, PlacementHeader, PlacementCookie:
	default:
		return E.New("unsupported xhttp uplink data placement: ", c.uplinkDataPlacement)
	}
	if c.uplinkDataPlacement != PlacementBody && c.uplinkDataKey == "" {
		return E.New("missing xhttp uplink data key")
	}
	if !isMetaPlacement(c.sessionPlacement) {
		return E.New("unsupported xhttp session placement: ", c.sessionPlacement)
	}
	if !isMetaPlacement(c.seqPlacement) {
		return E.New("unsupported xhttp sequence placement: ", c.seqPlacement)
	}
	if c.xPaddingObfsMode {
		switch c.xPaddingPlacement {
		case PlacementHeader:
			if c.xPaddingHeader == "" {
				return E.New("missing xhttp padding header")
			}
		case PlacementQueryInHeader:
			if c.xPaddingHeader == "" || c.xPaddingKey == "" {
				return E.New("missing xhttp padding query-in-header key")
			}
		case PlacementCookie, PlacementQuery:
			if c.xPaddingKey == "" {
				return E.New("missing xhttp padding key")
			}
		default:
			return E.New("unsupported xhttp padding placement: ", c.xPaddingPlacement)
		}
	}
	return nil
}

func isMetaPlacement(placement string) bool {
	switch placement {
	case PlacementPath, PlacementQuery, PlacementHeader, PlacementCookie:
		return true
	default:
		return false
	}
}

func (c *xhttpConfig) applyMetaToRequest(request *http.Request, sessionID string, sequence string) {
	if sessionID != "" {
		switch c.sessionPlacement {
		case PlacementPath:
			request.URL.Path = appendToPath(request.URL.Path, sessionID)
		case PlacementQuery:
			query := request.URL.Query()
			query.Set(c.sessionKey, sessionID)
			request.URL.RawQuery = query.Encode()
		case PlacementHeader:
			request.Header.Set(c.sessionKey, sessionID)
		case PlacementCookie:
			request.AddCookie(&http.Cookie{Name: c.sessionKey, Value: sessionID})
		}
	}
	if sequence != "" {
		switch c.seqPlacement {
		case PlacementPath:
			request.URL.Path = appendToPath(request.URL.Path, sequence)
		case PlacementQuery:
			query := request.URL.Query()
			query.Set(c.seqKey, sequence)
			request.URL.RawQuery = query.Encode()
		case PlacementHeader:
			request.Header.Set(c.seqKey, sequence)
		case PlacementCookie:
			request.AddCookie(&http.Cookie{Name: c.seqKey, Value: sequence})
		}
	}
}

func (c *xhttpConfig) generateSessionID() (string, error) {
	if c.sessionTable != "" && c.sessionLengthTo > 0 {
		length := int(randRange(c.sessionLengthFrom, c.sessionLengthTo))
		identifier := make([]byte, length)
		for index := range identifier {
			identifier[index] = c.sessionTable[mrand.N(len(c.sessionTable))]
		}
		return string(identifier), nil
	}
	identifier, err := uuid.NewV4()
	if err != nil {
		return "", err
	}
	return identifier.String(), nil
}

func (c *xhttpConfig) getUplinkChunkSize() int {
	size := randRange(c.uplinkChunkSizeFrom, c.uplinkChunkSizeTo)
	if size < 64 {
		size = 64
	}
	return int(size)
}

func (c *xhttpConfig) applyXPaddingToRequest(request *http.Request, rawURL string) {
	length := randRange(c.xPaddingBytesFrom, c.xPaddingBytesTo)
	if length <= 0 {
		return
	}
	paddingValue := generatePadding(c.xPaddingMethod, int(length))
	if c.xPaddingObfsMode {
		switch c.xPaddingPlacement {
		case PlacementHeader:
			request.Header.Set(c.xPaddingHeader, paddingValue)
		case PlacementQueryInHeader:
			paddingURL, err := url.Parse(rawURL)
			if err != nil {
				return
			}
			paddingURL.RawQuery = c.xPaddingKey + "=" + paddingValue
			request.Header.Set(c.xPaddingHeader, paddingURL.String())
		case PlacementCookie:
			request.AddCookie(&http.Cookie{Name: c.xPaddingKey, Value: paddingValue, Path: "/"})
		case PlacementQuery:
			query := request.URL.Query()
			query.Set(c.xPaddingKey, paddingValue)
			request.URL.RawQuery = query.Encode()
		}
		return
	}
	paddingURL, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	paddingURL.RawQuery = "x_padding=" + paddingValue
	request.Header.Set("Referer", paddingURL.String())
}

func (c *xhttpConfig) encodeUplinkData(request *http.Request, data []byte) {
	encodedData := base64.RawURLEncoding.EncodeToString(data)
	chunkSize := c.getUplinkChunkSize()
	switch c.uplinkDataPlacement {
	case PlacementHeader, PlacementAuto:
		for index := 0; index < len(encodedData); index += chunkSize {
			end := min(index+chunkSize, len(encodedData))
			request.Header.Set(fmt.Sprintf("%s-%d", c.uplinkDataKey, index/chunkSize), encodedData[index:end])
		}
	case PlacementCookie:
		for index := 0; index < len(encodedData); index += chunkSize {
			end := min(index+chunkSize, len(encodedData))
			request.AddCookie(&http.Cookie{
				Name:  fmt.Sprintf("%s_%d", c.uplinkDataKey, index/chunkSize),
				Value: encodedData[index:end],
			})
		}
	}
}

func generatePadding(method string, length int) string {
	if length <= 0 {
		return ""
	}
	if method == PaddingMethodTokenish {
		return generateTokenishPadding(length)
	}
	return strings.Repeat("X", length)
}

func generateTokenishPadding(targetHuffmanBytes int) string {
	const (
		charset                = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		avgHuffmanBytesPerChar = 0.8
	)
	length := int(float64(targetHuffmanBytes)/avgHuffmanBytesPerChar + 0.5)
	if length < 1 {
		length = 1
	}
	result := make([]byte, length)
	randomBuffer := make([]byte, 256)
	resultIndex := 0
	limit := byte(256 - (256 % len(charset)))
	for resultIndex < length {
		if _, err := crand.Read(randomBuffer); err != nil {
			return strings.Repeat("X", targetHuffmanBytes)
		}
		for _, randomByte := range randomBuffer {
			if randomByte >= limit {
				continue
			}
			result[resultIndex] = charset[int(randomByte)%len(charset)]
			resultIndex++
			if resultIndex >= length {
				break
			}
		}
	}

	padding := string(result)
	for iteration := 0; iteration < 150; iteration++ {
		currentLength := int(hpack.HuffmanEncodeLength(padding))
		difference := currentLength - targetHuffmanBytes
		if difference >= -2 && difference <= 2 {
			return padding
		}
		if difference < 0 {
			padding += "X"
		} else if len(padding) > 1 {
			padding = padding[:len(padding)-1]
		} else {
			return padding
		}
	}
	return padding
}

func boundedPositiveRange(config *option.V2RayXHTTPRangeConfig, defaultFrom int, defaultTo int, minimum int, maximum int) (int32, int32) {
	from := int32(defaultFrom)
	to := int32(defaultTo)
	if config != nil && rangeHasPositiveValue(config) {
		from = config.From
		to = config.To
		if from <= 0 {
			from = to
		}
		if to <= 0 {
			to = from
		}
	}
	minimumValue := int32(minimum)
	maximumValue := int32(maximum)
	if from < minimumValue {
		from = minimumValue
	}
	if to < from {
		to = from
	}
	if from > maximumValue {
		from = maximumValue
	}
	if to > maximumValue {
		to = maximumValue
	}
	return from, to
}

func normalizeSessionTable(table string) string {
	if predefined, loaded := predefinedSessionTables[table]; loaded {
		return predefined
	}
	return table
}

var predefinedSessionTables = map[string]string{
	"ALPHABET": "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"Alphabet": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
	"BASE36":   "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"Base62":   "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
	"HEX":      "0123456789ABCDEF",
	"alphabet": "abcdefghijklmnopqrstuvwxyz",
	"base36":   "0123456789abcdefghijklmnopqrstuvwxyz",
	"hex":      "0123456789abcdef",
	"number":   "0123456789",
}

func applyDefaultFetchHeaders(header http.Header) {
	switch header.Get("User-Agent") {
	case "", "chrome":
		header.Set("User-Agent", defaultChromeUA())
	case "golang":
		header.Del("User-Agent")
	}
	if header.Get("User-Agent") != "" && header.Get("Sec-Fetch-Mode") == "" {
		header.Set("Sec-Fetch-Mode", "cors")
		header.Set("Sec-Fetch-Dest", "empty")
		header.Set("Sec-Fetch-Site", "same-origin")
	}
	if header.Get("Accept") == "" {
		header.Set("Accept", "*/*")
	}
	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "no-cache")
	}
	if header.Get("Pragma") == "" {
		header.Set("Pragma", "no-cache")
	}
	if header.Get("Priority") == "" {
		header.Set("Priority", "u=1, i")
	}
}

func defaultChromeUA() string {
	return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36", defaultChromeMajor)
}

func randRange(from, to int32) int32 {
	if from >= to {
		return from
	}
	span := int64(to) - int64(from) + 1
	randomValue, err := crand.Int(crand.Reader, big.NewInt(span))
	if err != nil {
		return from
	}
	return int32(int64(from) + randomValue.Int64())
}

func appendToPath(path, value string) string {
	if strings.HasSuffix(path, "/") {
		return path + value
	}
	return path + "/" + value
}
