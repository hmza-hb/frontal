"use client";

import { useState } from "react";
import { motion } from "framer-motion";
import { ArrowRight, Sparkles, CheckCircle2, Film, Lock } from "lucide-react";
import confetti from "canvas-confetti";

interface FinalCtaSectionProps {
  onOpenWaitlist: () => void;
}

export default function FinalCtaSection({ onOpenWaitlist }: FinalCtaSectionProps) {
  const [email, setEmail] = useState("");
  const [submitted, setSubmitted] = useState(false);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!email) return;
    setSubmitted(true);
    confetti({
      particleCount: 100,
      spread: 80,
      origin: { y: 0.7 },
      colors: ["#0f172a", "#b45309", "#10b981"],
    });
  };

  return (
    <section className="py-24 md:py-32 bg-slate-50 border-b border-slate-200/60 relative overflow-hidden">
      
      {/* Subtle Background Glow */}
      <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[700px] h-[350px] bg-gradient-to-r from-amber-200/30 via-slate-200/50 to-amber-100/30 blur-3xl rounded-full pointer-events-none" />

      <div className="max-w-4xl mx-auto px-4 sm:px-6 relative z-10 text-center space-y-8">
        
        {/* Section Badge */}
        <div className="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-white border border-slate-200 shadow-sm text-xs font-mono text-slate-700">
          <Sparkles className="w-3.5 h-3.5 text-amber-600" />
          <span>— SECTION 7: FINAL CTA</span>
        </div>

        {/* Headline */}
        <h2 className="text-3xl sm:text-4xl md:text-5xl font-extrabold tracking-tight text-slate-900 leading-tight">
          Be Among the First to Shape the Future of Entertainment.
        </h2>

        {/* Body */}
        <p className="text-lg text-slate-600 font-normal leading-relaxed max-w-2xl mx-auto">
          Join the Big Film Fund community and be the first to discover upcoming opportunities, company updates, and the future of entertainment investing.
        </p>

        {/* Form Container */}
        <div className="max-w-md mx-auto pt-4">
          {!submitted ? (
            <form onSubmit={handleSubmit} className="space-y-3">
              <div className="flex flex-col sm:flex-row items-center gap-2 bg-white p-2 rounded-2xl border border-slate-200 shadow-xl">
                <input
                  type="email"
                  required
                  placeholder="Enter your email address..."
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  className="w-full px-4 py-3 bg-transparent text-slate-900 text-sm focus:outline-none placeholder:text-slate-400"
                />
                <button
                  type="submit"
                  className="w-full sm:w-auto px-6 py-3 rounded-xl bg-slate-900 text-white font-semibold text-xs uppercase tracking-wider hover:bg-slate-800 transition-all shrink-0 flex items-center justify-center gap-2 shadow-md"
                >
                  <span>Join Waitlist</span>
                  <ArrowRight className="w-4 h-4" />
                </button>
              </div>

              <div className="flex items-center justify-center gap-4 text-xs text-slate-500 font-mono pt-1">
                <span className="flex items-center gap-1">
                  <Lock className="w-3 h-3 text-slate-400" />
                  <span>No commitment. Just early access.</span>
                </span>
              </div>
            </form>
          ) : (
            <div className="p-6 rounded-2xl bg-white border border-slate-200 shadow-xl space-y-3">
              <div className="w-10 h-10 rounded-full bg-emerald-100 text-emerald-600 flex items-center justify-center mx-auto">
                <CheckCircle2 className="w-6 h-6" />
              </div>
              <h4 className="font-bold text-slate-900">Spot Reserved!</h4>
              <p className="text-xs text-slate-600">
                Thank you for joining the Big Film Fund early access waitlist. We will reach out with founder updates soon.
              </p>
            </div>
          )}
        </div>

        {/* Secondary Modal Trigger Link */}
        <div className="pt-2">
          <button
            onClick={onOpenWaitlist}
            className="text-xs font-mono text-slate-500 hover:text-slate-900 underline underline-offset-4 transition-colors"
          >
            Prefer to select Creator or Investor role? Click here to customize access.
          </button>
        </div>

      </div>
    </section>
  );
}
