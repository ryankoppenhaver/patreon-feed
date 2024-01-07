package main

import (
	_ "embed"
	"encoding/json"
	"encoding/xml"
  "fmt"
	"io"
  "log/slog"
	"net/http"
  "net/url"
  "os"
  "regexp"
	"strconv"
	"time"

  "github.com/gin-gonic/gin"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

const XMLPrefix = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"
const AtomType = "application/atom+xml"
const HTMLType = "text/html"

const campaignUrlTemplate = "https://www.patreon.com/api/campaigns/%d"
const postsUrlTemplate = "https://www.patreon.com/api/posts?fields[post]=title,url,teaser_text,content,published_at&filter[campaign_id]=%d&filter[contains_exclusive_posts]=true&filter[is_draft]=false&sort=-published_at&json-api-version=1.0&json-api-use-default-includes=false"
const searchUrlTemplate = "https://www.patreon.com/api/search?q=%s&page%%5Bsize%%5D=5&json-api-version=1.0&include=[]"


type CampaignAPIResponse struct {
  Data struct {
    Attributes struct {
      Name string
      URL string
    }
  }
}

type PostsAPIResponse struct {
	Data []Post
}

type Post struct {
	Attributes PostAttributes
	ID         string
}

type PostAttributes struct {
  Content string
  PublishedAt time.Time `json:"published_at"`
  TeaserText string `json:"teaser_text"`
	Title string
	URL   string
}

type SearchAPIResponse struct {
  Data []struct {
    Attributes struct {
      CreatorName string `json:"creator_name"`
      CreationName string `json:"creation_name"`
      URL string
    }
    ID string `json:"id"`
  }
}

type FrontendSearchResult struct {
  Name string `json:"name"`
  Desc string `json:"desc"`
  ID string `json:"id"`
  URL string `json:"url"`
}

type Feed struct {
	XMLName xml.Name `xml:"feed"`
  XMLNS   string `xml:"xmlns,attr"`

  //Author  string `xml:"author"` //required by the spec, but blank is also wrong, so just leave it off for now
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Updated string `xml:"updated"`
	Link    []Link `xml:"link"`

	Entries []FeedEntry `xml:"entry"`
}

type FeedContent struct{
  Type string `xml:"type,attr"`
  Content string `xml:",chardata"`
}

type FeedEntry struct{
  Title string `xml:"title"`
  Content FeedContent `xml:"content"`
  Link Link `xml:"link"`
  Updated time.Time `xml:"updated"`
}

type Link struct{
  Rel string `xml:"rel,attr"`
  Type string `xml:"type,attr"`
  HRef string `xml:"href,attr"`
}

//go:embed home.html
var homeHTML string

var campaignCache = expirable.NewLRU[int, *CampaignAPIResponse](1000, nil, 24*time.Hour)
var postsCache = expirable.NewLRU[int, *PostsAPIResponse](1000, nil, 15*time.Minute)
var searchCache = expirable.NewLRU[string, *SearchAPIResponse](1000, nil, 1*time.Hour)

func main() {
  //todo prod config
  slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
  })))

  router := gin.New()
  router.Use(gin.LoggerWithConfig(gin.LoggerConfig{
    SkipPaths: []string{"/favicon.ico"},
  }))
  router.Use(gin.Recovery())
    
  router.GET("/", handleHome)
  router.GET("/feed/:id", handleFeed)
  router.GET("/search", handleSearch)

  router.SetTrustedProxies(nil)

  router.Run(":8000")
}

func handleHome(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/html")
	io.WriteString(c.Writer, homeHTML)
}

//go:embed search1.json
var test []byte

var campaignIDPattern = regexp.MustCompile(`^campaign_(\d+)$`)

func handleSearch(c *gin.Context) {
  //q := c.Query("q")

  results := SearchAPIResponse{}
  err := json.Unmarshal(test, &results)

  fmt.Printf("%+v\n", results)

  //results, err := fetchWithCache(searchUrlTemplate, url.QueryEscape(q), searchCache)
  if err != nil {
    fail(c, "search", err)
    return
  }

  res := make([]FrontendSearchResult, len(results.Data))
  for idx, item := range results.Data {
    matches := campaignIDPattern.FindStringSubmatch(item.ID)
    if matches == nil || len(matches) < 2 {
      fail(c, "search results", fmt.Errorf("bad id: %s", item.ID))
    }

    res[idx].Name = item.Attributes.CreatorName
    res[idx].Desc = item.Attributes.CreationName
    res[idx].ID = matches[1]
    res[idx].URL = item.Attributes.URL
  }

  c.JSON(200, res)
}


func handleFeed(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	if id == 0 {
		c.Writer.WriteHeader(400)
		io.WriteString(c.Writer, "bad id")
		return
	}

  campaign, err := fetchWithCache(campaignUrlTemplate, id, campaignCache)
	if err != nil {
    fail(c, "fetch campaign", err)
    return
  }
  
  posts, err := fetchWithCache(postsUrlTemplate, id, postsCache)
	if err != nil {
    fail(c, "fetch posts", err)
		return
	}

  entries := make([]FeedEntry, len(posts.Data))
  for idx, post := range posts.Data {
    fc := FeedContent{}
    if post.Attributes.Content != "" {
      fc.Type = "html"
      fc.Content = post.Attributes.Content
    } else {
      fc.Type = "text"
      fc.Content = post.Attributes.TeaserText
    }

    entries[idx] = FeedEntry{
      Title: post.Attributes.Title,
      Content: fc,
      Link: Link{
        Rel: "alternate",
        Type: HTMLType,
        HRef: post.Attributes.URL,
      },
      Updated: post.Attributes.PublishedAt,
    }    
  }

	feed := Feed{
    XMLNS: "http://www.w3.org/2005/Atom",
		ID: fullURL(c.Request).String(),
    Title: fmt.Sprintf("Patreon: %s", campaign.Data.Attributes.Name),
    Link: []Link{{
      Rel: "alternate",
      Type: HTMLType,
      HRef: campaign.Data.Attributes.URL,
    }, {
      Rel: "self",
      Type: AtomType,
      HRef: fullURL(c.Request).String(),
    }},
    Updated: time.Now().UTC().Format(time.RFC3339),
		Entries: entries,
	}

	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
    fail(c, "marshal", err)
		return
	}

  io.WriteString(c.Writer, XMLPrefix)
	c.Writer.Write(out) //todo err
}

func fullURL(r *http.Request) *url.URL {
  scheme := "http"
  if r.TLS != nil {
    scheme = "https"
  }
  base := url.URL{
    Scheme: scheme,
    Host: r.Host,
  }
  return base.ResolveReference(r.URL)
}

func fetch(url string) ([]byte, error) {
  resp, err := http.Get(url)
  if err !=  nil {
    return nil, fmt.Errorf("get %s: %w", url, err)
  }

  defer resp.Body.Close()
  body, err := io.ReadAll(resp.Body)


  if err != nil {
    return nil, fmt.Errorf("get %s: read body: %v", err)
  }

  if resp.StatusCode != 200 {
    return nil, fmt.Errorf("get %s: status %s: %s", url, resp.Status, string(body))
  }

  slog.Debug("fetched", "url", url, "res body", string(body))

  return body, nil
}

func fetchWithCache[K comparable, S any](urlTemplate string, key K, cache *expirable.LRU[K, *S]) (*S, error) {
  s, ok := cache.Get(key)
  if ok {
    slog.Debug("cache hit", "key", key)
    return s, nil
  }

  slog.Debug("cache miss", "key", key)

  body, err := fetch(fmt.Sprintf(urlTemplate, key))
  if err != nil {
    return nil, err
  }

  s = new(S)
  err = json.Unmarshal(body, s)
  if err != nil {
    return nil, err
  }

  cache.Add(key, s)
  return s, nil
}

func fail(c *gin.Context, context string, err error) {
  c.Writer.WriteHeader(500)
  io.WriteString(c.Writer, fmt.Sprintf("internal error: %s: %v", context, err))
}

