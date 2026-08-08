"use client";

import { useState, useEffect } from "react";
import { Film, Menu, X, ArrowUpRight } from "lucide-react";

interface NavbarProps {
  onOpenWaitlist: () => void;
}

export default function Navbar({ onOpenWaitlist }: NavbarProps) {
  const [scrolled, setScrolled] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);

  useEffect(() => {
    const handleScroll = () => {
      setScrolled(window.scrollY > 20);
    };
    window.addEventListener("scroll", handleScroll);
    return () => window.removeEventListener("scroll", handleScroll);
  }, []);

  const navLinks = [
    { name: "Problem", href: "#problem" },
    { name: "Vision", href: "#vision" },
    { name: "How It Works", href: "#how-it-works" },
    { name: "Why BFF", href: "#why-bff" },
    { name: "Why Now", href: "#why-now" },
  ];

  return (
    <header className="fixed top-4 inset-x-0 z-40 px-4 sm:px-6 pointer-events-none">
      <div className="max-w-6xl mx-auto flex items-center justify-between">
        {/* Main Floating Pill Header */}
        <div
          className={`pointer-events-auto w-full flex items-center justify-between px-4 sm:px-6 py-3 rounded-full transition-all duration-300 ${
            scrolled
              ? "glass-pill shadow-lg shadow-slate-900/5 border-slate-200/80"
              : "glass-pill shadow-sm border-slate-200/50"
          }`}
        >
          {/* Brand Logo */}
          <a href="#" className="flex items-center gap-2.5 group">
            <div className="w-8 h-8 rounded-full bg-slate-900 flex items-center justify-center text-white group-hover:scale-105 transition-transform">
              <Film className="w-4 h-4 text-amber-400" />
            </div>
            <div className="flex flex-col">
              <span className="font-bold text-sm tracking-tight text-slate-900 leading-none">
                BFF
              </span>
              <span className="text-[10px] font-mono tracking-widest text-slate-500 uppercase leading-tight">
                Big Film Fund
              </span>
            </div>
          </a>

          {/* Desktop Nav Links */}
          <nav className="hidden md:flex items-center gap-6">
            {navLinks.map((link) => (
              <a
                key={link.name}
                href={link.href}
                className="text-xs font-medium text-slate-600 hover:text-slate-900 transition-colors"
              >
                {link.name}
              </a>
            ))}
          </nav>

          {/* Desktop CTA Action */}
          <div className="hidden sm:flex items-center gap-3">
            <button
              onClick={onOpenWaitlist}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-full bg-slate-900 text-white text-xs font-semibold hover:bg-slate-800 transition-all hover:shadow-md hover:shadow-slate-900/10 active:scale-95"
            >
              <span>Join the Waitlist</span>
              <ArrowUpRight className="w-3.5 h-3.5" />
            </button>
          </div>

          {/* Mobile Menu Button */}
          <button
            onClick={() => setMobileMenuOpen(!mobileMenuOpen)}
            className="md:hidden p-2 rounded-full text-slate-700 hover:bg-slate-100 transition-colors"
            aria-label="Toggle menu"
          >
            {mobileMenuOpen ? <X className="w-5 h-5" /> : <Menu className="w-5 h-5" />}
          </button>
        </div>
      </div>

      {/* Mobile Dropdown Menu */}
      {mobileMenuOpen && (
        <div className="pointer-events-auto md:hidden mt-2 max-w-6xl mx-auto rounded-2xl bg-white/95 backdrop-blur-md p-5 border border-slate-200 shadow-xl space-y-3">
          <nav className="flex flex-col space-y-2">
            {navLinks.map((link) => (
              <a
                key={link.name}
                href={link.href}
                onClick={() => setMobileMenuOpen(false)}
                className="px-3 py-2 rounded-lg text-sm font-medium text-slate-700 hover:bg-slate-50 transition-colors"
              >
                {link.name}
              </a>
            ))}
          </nav>
          <div className="pt-2 border-t border-slate-100">
            <button
              onClick={() => {
                setMobileMenuOpen(false);
                onOpenWaitlist();
              }}
              className="w-full py-2.5 rounded-xl bg-slate-900 text-white text-xs font-semibold flex items-center justify-center gap-2"
            >
              <span>Join the Waitlist</span>
              <ArrowUpRight className="w-4 h-4" />
            </button>
          </div>
        </div>
      )}
    </header>
  );
}
