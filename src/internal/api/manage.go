package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ranaroussi/minigun/internal/models"
	"github.com/ranaroussi/minigun/internal/store"
	"github.com/ranaroussi/minigun/internal/tmpl"
	"github.com/ranaroussi/minigun/internal/token"
)

type manageContext struct {
	Token     string
	Send      *models.Send
	List      *models.List
	Contact   *models.Contact
	Sub       *models.Subscription
	Company   *models.Company
}

func (s *Server) loadManageContext(ctx context.Context, tokenStr string) (*manageContext, error) {
	t, err := token.Verify(s.cfg.HMACSecret, tokenStr)
	if err != nil {
		return nil, errors.New("Invalid or expired manage link.")
	}
	snd, err := s.store.GetSend(ctx, t.SendID)
	if err != nil {
		return nil, errors.New("Send not found.")
	}
	sub, err := s.store.GetSubscriptionByID(ctx, t.SubscriptionID)
	if err != nil {
		return nil, errors.New("Subscription not found.")
	}
	contact, err := s.store.GetContactByID(ctx, sub.ContactID)
	if err != nil {
		return nil, errors.New("Contact not found.")
	}
	list, err := s.store.GetListByID(ctx, sub.ListID)
	if err != nil {
		return nil, errors.New("List not found.")
	}
	if list.CompanyID == "" {
		return nil, errors.New("This list is not associated with a company; manage page is not available.")
	}
	company, err := s.store.GetCompanyByID(ctx, list.CompanyID)
	if err != nil {
		return nil, errors.New("Company not found.")
	}
	return &manageContext{
		Token:   tokenStr,
		Send:    snd,
		List:    list,
		Contact: contact,
		Sub:     sub,
		Company: company,
	}, nil
}

func (s *Server) handleManageGet(w http.ResponseWriter, r *http.Request) {
	tokenStr := chi.URLParam(r, "token")
	mc, err := s.loadManageContext(r.Context(), tokenStr)
	if err != nil {
		s.renderManagePage(w, tmpl.ManageData{Error: err.Error()})
		return
	}
	states, err := s.store.GetCompanyListsWithSubscription(r.Context(), mc.Company.ID, mc.Contact.ID)
	if err != nil {
		s.renderManagePage(w, tmpl.ManageData{Error: "Failed to load preferences."})
		return
	}
	s.renderManagePage(w, tmpl.ManageData{
		Token:       tokenStr,
		Email:       mc.Contact.Email,
		CompanyName: mc.Company.Name,
		Lists:       states,
	})
}

func (s *Server) handleManagePost(w http.ResponseWriter, r *http.Request) {
	tokenStr := chi.URLParam(r, "token")
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mc, err := s.loadManageContext(r.Context(), tokenStr)
	if err != nil {
		s.renderManagePage(w, tmpl.ManageData{Error: err.Error()})
		return
	}

	companyLists, err := s.store.ListsForCompany(r.Context(), mc.Company.ID)
	if err != nil {
		s.renderManagePage(w, tmpl.ManageData{Error: "Failed to load preferences."})
		return
	}

	checked := map[string]struct{}{}
	for _, v := range r.PostForm["list"] {
		checked[v] = struct{}{}
	}

	desired := make([]store.SubscriptionChange, 0, len(companyLists))
	for _, l := range companyLists {
		_, ok := checked[l.ID]
		desired = append(desired, store.SubscriptionChange{
			ListID:     l.ID,
			Subscribed: ok,
		})
	}

	deltas, err := s.store.ApplySubscriptionChanges(r.Context(), mc.Contact.ID, desired)
	if err != nil {
		s.log.Error("apply subscription changes", "err", err)
		s.renderManagePage(w, tmpl.ManageData{Error: "Failed to save preferences."})
		return
	}

	for _, d := range deltas {
		if d.WasSubbed && !d.NowSubbed {
			sub, err := s.store.GetSubscription(r.Context(), d.ListID, mc.Contact.ID)
			if err != nil {
				continue
			}
			if _, err := s.store.RecordUnsubscribeEvent(r.Context(), &mc.Send.ID, sub, mc.Contact.Email); err != nil {
				s.log.Error("record unsub event from /manage", "err", err)
			}
		}
	}

	s.renderManagePage(w, tmpl.ManageData{
		Done:        true,
		Email:       mc.Contact.Email,
		CompanyName: mc.Company.Name,
		Deltas:      deltas,
	})
}

func (s *Server) renderManagePage(w http.ResponseWriter, data tmpl.ManageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	if err := tmpl.Manage.Execute(w, data); err != nil {
		s.log.Error("render manage page", "err", err)
	}
}
