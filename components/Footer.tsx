"use client";

import { Film, ArrowUpRight } from "lucide-react";

interface FooterProps {
  onOpenWaitlist: () => void;
}

export default function Footer({ onOpenWaitlist }: FooterProps) {
  return (
    <footer className="bg-white border-t border-slate-200 py-16">
      <div className="max-w-6xl mx-auto px-4 sm:px-6 space-y-12">
        
        {/* Top Grid */}
        <div className="grid grid-cols-1 md:grid-cols-12 gap-8 items-start">
          
          {/* Brand & Positioning */}
          <div className="md:col-span-6 space-y-4">
            <div className="flex items-center gap-2.5">
              <div className="w-8 h-8 rounded-full bg-slate-900 flex items-center justify-center text-white">
                <Film className="w-4 h-4 text-amber-400" />
              </div>
              <span className="font-extrabold text-lg tracking-tight text-slate-900">
                Big Film Fund
              </span>
            </div>

            {/* Crucial Footer Positioning Line */}
            <p className="text-base font-semibold text-slate-800 tracking-tight">
              Big Film Fund — Building the future of entertainment capital.
            </p>

            <p className="text-xs text-slate-500 max-w-md leading-relaxed">
              Connecting visionary creators with a global community to discover, support, and participate in the next generation of film production.
            </p>
          </div>

          {/* Quick Links */}
          <div className="md:col-span-3 space-y-3">
            <div className="text-xs font-mono text-slate-400 uppercase tracking-wider">
              Navigation
            </div>
            <ul className="space-y-2 text-xs font-medium text-slate-600">
              <li><a href="#problem" className="hover:text-slate-900 transition-colors">The Problem</a></li>
              <li><a href="#vision" className="hover:text-slate-900 transition-colors">The Vision</a></li>
              <li><a href="#how-it-works" className="hover:text-slate-900 transition-colors">How It Works</a></li>
              <li><a href="#why-bff" className="hover:text-slate-900 transition-colors">Why BFF</a></li>
              <li><a href="#why-now" className="hover:text-slate-900 transition-colors">Why Now</a></li>
            </ul>
          </div>

          {/* Contact / Access */}
          <div className="md:col-span-3 space-y-3">
            <div className="text-xs font-mono text-slate-400 uppercase tracking-wider">
              Early Access
            </div>
            <button
              onClick={onOpenWaitlist}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-slate-900 text-white text-xs font-semibold hover:bg-slate-800 transition-colors"
            >
              <span>Join Waitlist</span>
              <ArrowUpRight className="w-3.5 h-3.5" />
            </button>
            <p className="text-[11px] text-slate-400 font-mono">
              Status: Private Founder Waitlist Active
            </p>
          </div>

        </div>

        {/* Bottom Legal & Positioning Bar */}
        <div className="pt-8 border-t border-slate-100 flex flex-col sm:flex-row items-center justify-between gap-4 text-xs text-slate-400 font-mono">
          <div>
            © {new Date().getFullYear()} Big Film Fund Inc. All rights reserved.
          </div>
          <div className="flex items-center gap-4">
            <span>Privacy Policy</span>
            <span>•</span>
            <span>Terms of Access</span>
            <span>•</span>
            <span>Disclaimers</span>
          </div>
        </div>

      </div>
    </footer>
  );
}
