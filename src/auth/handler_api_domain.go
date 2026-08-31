package auth

import (
	"net/http"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// userDomainOwner resolves the caller as a domain owner.
func userDomainOwner(r *http.Request) (DomainOwner, int64, *apperr.Error) {
	u, found := UserFrom(r.Context())
	if !found {
		return DomainOwner{}, 0, ErrUnauthenticated()
	}
	return DomainOwner{Type: OwnerUser, ID: u.ID}, u.ID, nil
}

// orgDomainOwner resolves the request's organization as a domain owner. The org came
// from RequireOrgRole, which already proved the caller is a member with a managing role.
func orgDomainOwner(r *http.Request) (DomainOwner, int64, *apperr.Error) {
	u, org, aerr := orgContext(r)
	if aerr != nil {
		return DomainOwner{}, 0, aerr
	}
	return DomainOwner{Type: OwnerOrg, ID: org.ID}, u.ID, nil
}

// domainOwnerFunc resolves the owner tuple and the acting user for a route.
type domainOwnerFunc func(*http.Request) (DomainOwner, int64, *apperr.Error)

// listDomains is the shared listing handler for both owner kinds.
func (s *Service) listDomains(resolve domainOwnerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, _, aerr := resolve(r)
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		rows, aerr := s.ListDomains(r.Context(), owner)
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		ok(w, r, s.publicDomains(rows))
	}
}

// addDomain is the shared creation handler for both owner kinds.
func (s *Service) addDomain(resolve domainOwnerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, actorID, aerr := resolve(r)
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		var body struct {
			Domain string `json:"domain"`
		}
		if aerr := bind(w, r, &body, func(r *http.Request) {
			body.Domain = r.PostFormValue("domain")
		}); aerr != nil {
			fail(w, r, aerr)
			return
		}
		d, aerr := s.AddDomain(r.Context(), owner, actorID, body.Domain)
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		created(w, r, s.publicDomain(d))
	}
}

// getDomain is the shared read handler for both owner kinds.
func (s *Service) getDomain(resolve domainOwnerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, _, aerr := resolve(r)
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		d, aerr := s.GetDomain(r.Context(), owner, r.PathValue("domain"))
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		ok(w, r, s.publicDomain(d))
	}
}

// verifyDomain is the shared ownership check handler for both owner kinds.
func (s *Service) verifyDomain(resolve domainOwnerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, actorID, aerr := resolve(r)
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		d, aerr := s.VerifyDomain(r.Context(), owner, actorID, r.PathValue("domain"))
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		ok(w, r, s.publicDomain(d))
	}
}

// deleteDomain is the shared removal handler for both owner kinds.
func (s *Service) deleteDomain(resolve domainOwnerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, actorID, aerr := resolve(r)
		if aerr != nil {
			fail(w, r, aerr)
			return
		}
		if aerr := s.DeleteDomain(r.Context(), owner, actorID, r.PathValue("domain")); aerr != nil {
			fail(w, r, aerr)
			return
		}
		ok(w, r, messageOnly{Message: "Domain removed"})
	}
}
