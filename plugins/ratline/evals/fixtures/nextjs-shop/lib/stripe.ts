import Stripe from "stripe";
export const stripe = new Stripe(process.env.STRIPE_SECRET_KEY!);
export const publishableKey = process.env.NEXT_PUBLIC_STRIPE_PUBLISHABLE_KEY;
export const siteUrl = process.env.NEXT_PUBLIC_SITE_URL ?? "http://localhost:3000";
