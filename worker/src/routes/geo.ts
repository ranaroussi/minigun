import type { Hono } from 'hono';

interface GeoRequest extends Request {
  cf?: {
    country?: string;
    city?: string;
    region?: string;
    timezone?: string;
  };
}

export function mountGeo(app: Hono) {
  app.get('/geo', (c) => {
    const req = c.req.raw as GeoRequest;
    return c.json({
      country: req.cf?.country,
      city: req.cf?.city,
      region: req.cf?.region,
      timezone: req.cf?.timezone,
    });
  });

  app.get('/full', (c) => {
    const req = c.req.raw as GeoRequest;
    return c.json({
      ip: c.req.header('CF-Connecting-IP'),
      country: req.cf?.country,
      city: req.cf?.city,
      region: req.cf?.region,
      timezone: req.cf?.timezone,
    });
  });
}
