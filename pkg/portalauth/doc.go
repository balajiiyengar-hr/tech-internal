// Package portalauth is the embeddable client library for the tech-internal
// auth scheme. Other Go services import it instead of calling the auth API
// on every request.
//
// Java-style @authenticate / @authorize map to middleware:
//
//	// @authenticate — signature + expiry
//	r.Use(ginmw.Authenticate([]byte(os.Getenv("JWT_SECRET"))))
//
//	// @authorize role=admin resource=users action=write
//	admin.Use(ginmw.Authorize(
//	    portalauth.RequireRole("admin"),
//	    portalauth.RequireResource("users", "write"),
//	))
//
// Authenticate only checks JWT integrity and exp. Authorize checks user id,
// roles, scopes, and resource access on the already-verified payload.
package portalauth
