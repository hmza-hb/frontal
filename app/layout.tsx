import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Big Film Fund (BFF) — The Future of Entertainment Funding Starts Here",
  description:
    "Big Film Fund is building a new way to finance entertainment — connecting visionary creators with a global community to discover, support, and participate in the next generation of films.",
  keywords: [
    "Big Film Fund",
    "BFF",
    "Film Financing",
    "Entertainment Capital",
    "Film Ownership",
    "Creator Economy",
    "Cinema Investment",
  ],
  openGraph: {
    title: "Big Film Fund (BFF) — The Future of Entertainment Funding",
    description:
      "Connecting visionary creators with a global community to discover, support, and participate in groundbreaking films.",
    type: "website",
    siteName: "Big Film Fund",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className={`${geistSans.variable} ${geistMono.variable} scroll-smooth`}>
      <body className="min-h-screen bg-slate-50 text-slate-900 antialiased selection:bg-slate-900 selection:text-white">
        {children}
      </body>
    </html>
  );
}
