# Deploy and Host SellingBot on Railway

SellingBot (AutoKart Admin) is a Go monolith for car dealerships: REST API, WhatsApp sales bot with QR pairing, 60-second follow-up scheduler, and an embedded admin UI covering leads, inventory, test drives, inspections, bookings, manual payment logging, finance enquiries, and sell/exchange valuations.

## About Hosting SellingBot

Hosting SellingBot takes one app service built from its Dockerfile (start command `/app/server`, healthcheck `/api/health`), one managed PostgreSQL database, and one 512MB persistent volume mounted at `/data`. Migrations auto-run on boot and the app retries the database connection until Postgres is ready. No environment variables are required: the JWT secret is generated once into the volume and the admin password is generated at first boot and printed once in the deploy logs. Run exactly one replica, since the WhatsApp socket and scheduler are single-process.

## Common Use Cases

- WhatsApp car sales: automatic replies, lead capture, model matching, and test-drive booking directly in chat.
- Showroom operations: vehicle inventory with photos, sell inspections, bookings, offline payment log, and finance plus trade-in valuation queues.
- Small-team CRM: sales and admin roles, assigned-lead pipelines, follow-up reminders, reviews, and a no-phone simulator for testing the bot.

## Dependencies for SellingBot Hosting

- Managed PostgreSQL database, wired via the `DATABASE_URL` reference variable.
- Persistent volume mounted at `/data` (car photos, WhatsApp session file, generated JWT secret).

## Why Deploy SellingBot on Railway?

Railway is a singular platform to deploy your infrastructure stack. Railway will host your infrastructure so you don't have to deal with configuration, while allowing you to vertically and horizontally scale it.

By deploying SellingBot on Railway, you are one step closer to supporting a complete full-stack application with minimal burden. Host your servers, databases, AI agents, and more on Railway.
