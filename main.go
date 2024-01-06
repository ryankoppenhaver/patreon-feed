package main

import (
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"io"
	"log"
	"net/http"
  "net/url"
	"strconv"
	"time"
  "fmt"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

const XMLPrefix = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"
const AtomType = "application/atom+xml"
const HTMLType = "text/html"

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



type Feed struct {
	XMLName xml.Name `xml:"feed"`
  XMLNS   string `xml:"xmlns,attr"`

  Author  string `xml:"author"` //nominally required by the spec, content todo
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

//go:embed out.json
var testJSON []byte
//go:embed campaign.json
var testCampaignJSON []byte

var campaignCache = expirable.NewLRU[int, *CampaignAPIResponse](1000, nil, 24*time.Hour)
var postsCache = expirable.NewLRU[int, *PostsAPIResponse](1000, nil, 15*time.Minute)

func main() {
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/feed", handleFeed)

	log.Fatal(http.ListenAndServe(":8000", nil))
}

func handleHome(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	io.WriteString(w, homeHTML)
}

func handleFeed(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))

	if id == 0 {
		w.WriteHeader(400)
		io.WriteString(w, "bad/missing param: id")
		return
	}

  campaign, err := fetchCampaign(id)
	if err != nil {
    fail(w, "fetch campaign", err)
    return
  }

  posts, err := fetchPosts(id)
	if err != nil {
    fail(w, "fetch posts", err)
		return
	}

  entries := make([]FeedEntry, len(posts.Data))
  for idx, post := range posts.Data {
    fmt.Printf("%+v\n\n", post.Attributes)

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
		ID: fullURL(r).String(),
    Title: fmt.Sprintf("Patreon: %s", campaign.Data.Attributes.Name),
    Link: []Link{{
      Rel: "alternate",
      Type: HTMLType,
      HRef: campaign.Data.Attributes.URL,
    }, {
      Rel: "self",
      Type: AtomType,
      HRef: fullURL(r).String(),
    }},
    Updated: time.Now().UTC().Format(time.RFC3339),
		Entries: entries,
	}

	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
    fail(w, "marshal", err)
		return
	}

  io.WriteString(w, XMLPrefix)
	w.Write(out) //todo err
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

  return body, nil
}

func fetchCampaign(id int) (*CampaignAPIResponse, error) {
  campaign, ok := campaignCache.Get(id)
  if ok {
    return campaign, nil
  }

  body := testCampaignJSON
  err := error(nil)
  //body, err := fetch(fmt.Sprintf("https://www.patreon.com/api/campaigns/%d", id))
  //if err != nil {
  //  return nil, err
  //}

  campaign = &CampaignAPIResponse{}
  err = json.Unmarshal(body, campaign)
  if err != nil {
    return nil, err
  }

  campaignCache.Add(id, campaign)
  return campaign, nil
}

func fetchPosts(id int) (*PostsAPIResponse, error) {
  posts, ok := postsCache.Get(id)
  if ok {
    return posts, nil
  }

  //body := testJSON
  //err := error(nil)
  body, err := fetch(fmt.Sprintf("https://www.patreon.com/api/posts?fields[post]=title,url,teaser_text,content,published_at&filter[campaign_id]=%d&filter[contains_exclusive_posts]=true&filter[is_draft]=false&sort=-published_at&json-api-version=1.0&json-api-use-default-includes=false", id))
  if err != nil {
    return nil, err
  }

  posts = &PostsAPIResponse{}
  err = json.Unmarshal(body, posts)
  if err != nil {
    return nil, err
  }

  postsCache.Add(id, posts)
  return posts, nil
}

func fail(w http.ResponseWriter, context string, err error) {
  w.WriteHeader(500)
  io.WriteString(w, fmt.Sprintf("internal error: %s: %v", context, err))
}
