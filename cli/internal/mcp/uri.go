package mcp

import (
	"errors"
	"net/url"
	"strings"
)

const URIScheme = "minigun"

type resourceKind int

const (
	kindListIndex resourceKind = iota + 1
	kindListItem
	kindListContacts
	kindSendIndex
	kindSendItem
	kindSendStats
	kindCompanyIndex
	kindCompanyItem
	kindCompanyLists
)

type parsedURI struct {
	kind    resourceKind
	slug    string
	sendID  string
	company string
	cursor  string
	limit   string
}

func parseURI(raw string) (parsedURI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return parsedURI{}, errors.New("invalid resource URI")
	}
	if u.Scheme != URIScheme {
		return parsedURI{}, errors.New("unsupported scheme: " + u.Scheme)
	}
	if u.Host != "" && u.Host != "lists" && u.Host != "sends" && u.Host != "companies" {
		return parsedURI{}, errors.New("unsupported host: " + u.Host)
	}

	host := u.Host
	path := strings.TrimPrefix(u.Path, "/")
	var segments []string
	if path != "" {
		segments = strings.Split(path, "/")
	}

	q := u.Query()
	cursor := q.Get("cursor")
	limit := q.Get("limit")

	switch host {
	case "lists":
		switch len(segments) {
		case 0:
			return parsedURI{kind: kindListIndex}, nil
		case 1:
			return parsedURI{kind: kindListItem, slug: segments[0]}, nil
		case 2:
			if segments[1] != "contacts" {
				return parsedURI{}, errors.New("unknown list sub-resource: " + segments[1])
			}
			return parsedURI{kind: kindListContacts, slug: segments[0], cursor: cursor, limit: limit}, nil
		default:
			return parsedURI{}, errors.New("unexpected path depth under minigun://lists")
		}
	case "sends":
		switch len(segments) {
		case 0:
			return parsedURI{kind: kindSendIndex, cursor: cursor, limit: limit}, nil
		case 1:
			return parsedURI{kind: kindSendItem, sendID: segments[0]}, nil
		case 2:
			if segments[1] != "stats" {
				return parsedURI{}, errors.New("unknown send sub-resource: " + segments[1])
			}
			return parsedURI{kind: kindSendStats, sendID: segments[0]}, nil
		default:
			return parsedURI{}, errors.New("unexpected path depth under minigun://sends")
		}
	case "companies":
		switch len(segments) {
		case 0:
			return parsedURI{kind: kindCompanyIndex}, nil
		case 1:
			return parsedURI{kind: kindCompanyItem, company: segments[0]}, nil
		case 2:
			if segments[1] != "lists" {
				return parsedURI{}, errors.New("unknown company sub-resource: " + segments[1])
			}
			return parsedURI{kind: kindCompanyLists, company: segments[0]}, nil
		default:
			return parsedURI{}, errors.New("unexpected path depth under minigun://companies")
		}
	}
	return parsedURI{}, errors.New("unknown resource")
}

func buildQuery(cursor, limit string) string {
	if cursor == "" && limit == "" {
		return ""
	}
	v := url.Values{}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	if limit != "" {
		v.Set("limit", limit)
	}
	return "?" + v.Encode()
}
