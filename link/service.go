package link

import (
	"webtyp.com/router"
	"webtyp.com/storage"

	"github.com/pyr33x/wisp/shared"
	"github.com/pyr33x/wisp/transport"
)

// Service holds link's dependencies. Every field is required.
type Service struct {
	Store   Store
	BaseURL string // public gateway origin; short URLs are BaseURL + "/" + code
	Random  shared.RandomSource
	Now     func() int64 // unix milliseconds
	Log     shared.Logger
	Secret  string // INTERNAL_SECRET
}

// Register mounts link's routes on r, all behind the internal-secret check.
// Routes are Public() as far as the router's identity gate is concerned:
// authentication between Workers is the RequireInternal middleware.
func (s *Service) Register(r router.Router) {
	r.Use(transport.RequireInternal(s.Secret, s.Log))
	r.Post(shared.PathLinks, s.create).Public()
	r.Get(shared.PathLink, s.resolve).Public()
	r.Get(shared.PathLinkExists, s.exists).Public()
	r.Delete(shared.PathLink, s.remove).Public()
}

func (s *Service) create(ctx router.Context) {
	var in shared.CreateLinkRequest
	if err := ctx.Decode(&in); err != nil {
		shared.WriteError(ctx, 400, shared.ErrCodeInvalidJSON, shared.MsgInvalidJSON)
		return
	}
	// gateway validated already; link checks again with no self-host so a
	// direct internal caller cannot store a non-http(s) URL either.
	origin, verr := shared.ValidateOrigin(in.URL, "")
	if verr != nil {
		shared.WriteError(ctx, 400, verr.Code, verr.Message)
		return
	}

	now := s.Now()
	for attempt := 0; attempt < shared.MaxCodeAttempts; attempt++ {
		code, err := shared.NewCode(s.Random)
		if err != nil {
			s.Log.Fail(500, ctx.Method(), ctx.Path(), err)
			shared.WriteError(ctx, 500, shared.ErrCodeInternal, shared.MsgInternal)
			return
		}
		// Insert and let the UNIQUE constraint decide: no check-then-insert race.
		err = s.Store.Insert(code, origin, now)
		if storage.IsDuplicate(err) {
			continue
		}
		if err != nil {
			s.Log.Fail(500, ctx.Method(), ctx.Path(), err)
			shared.WriteError(ctx, 500, shared.ErrCodeInternal, shared.MsgInternal)
			return
		}
		shared.WriteJSON(ctx, 201, &shared.Link{
			Code:      code,
			ShortURL:  shared.ShortURL(s.BaseURL, code),
			Origin:    origin,
			CreatedAt: now,
		})
		return
	}
	s.Log.Fail(500, ctx.Method(), ctx.Path(), errCodeSpaceExhausted)
	shared.WriteError(ctx, 500, shared.ErrCodeInternal, shared.MsgInternal)
}

func (s *Service) resolve(ctx router.Context) {
	code := ctx.Param(shared.ParamCode)
	if !shared.ValidCode(code) {
		shared.WriteError(ctx, 404, shared.ErrCodeNotFound, shared.MsgNotFound)
		return
	}
	row, found, err := s.Store.Active(code)
	if err != nil {
		s.Log.Fail(500, ctx.Method(), ctx.Path(), err)
		shared.WriteError(ctx, 500, shared.ErrCodeInternal, shared.MsgInternal)
		return
	}
	if !found {
		shared.WriteError(ctx, 404, shared.ErrCodeNotFound, shared.MsgNotFound)
		return
	}
	shared.WriteJSON(ctx, 200, &shared.Link{
		Code:      code,
		ShortURL:  shared.ShortURL(s.BaseURL, code),
		Origin:    row.Origin,
		CreatedAt: row.CreatedAt,
	})
}

func (s *Service) exists(ctx router.Context) {
	code := ctx.Param(shared.ParamCode)
	if !shared.ValidCode(code) {
		shared.WriteJSON(ctx, 200, &shared.Exists{Exists: false})
		return
	}
	ok, err := s.Store.Exists(code)
	if err != nil {
		s.Log.Fail(500, ctx.Method(), ctx.Path(), err)
		shared.WriteError(ctx, 500, shared.ErrCodeInternal, shared.MsgInternal)
		return
	}
	shared.WriteJSON(ctx, 200, &shared.Exists{Exists: ok})
}

func (s *Service) remove(ctx router.Context) {
	code := ctx.Param(shared.ParamCode)
	if !shared.ValidCode(code) {
		// Nothing with that shape can exist, so it is already "deleted".
		shared.WriteNoContent(ctx)
		return
	}
	if err := s.Store.SoftDelete(code, s.Now()); err != nil {
		s.Log.Fail(500, ctx.Method(), ctx.Path(), err)
		shared.WriteError(ctx, 500, shared.ErrCodeInternal, shared.MsgInternal)
		return
	}
	shared.WriteNoContent(ctx)
}
