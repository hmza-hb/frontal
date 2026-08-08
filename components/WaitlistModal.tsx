"use client";

import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { X, CheckCircle2, Film, Sparkles, ArrowRight } from "lucide-react";
import confetti from "canvas-confetti";

interface WaitlistModalProps {
  isOpen: boolean;
  onClose: () => void;
  defaultRole?: "backer" | "creator" | "investor";
}

export default function WaitlistModal({
  isOpen,
  onClose,
  defaultRole = "backer",
}: WaitlistModalProps) {
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<"backer" | "creator" | "investor">(defaultRole);
  const [submitted, setSubmitted] = useState(false);
  const [loading, setLoading] = useState(false);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!email) return;
    setLoading(true);

    setTimeout(() => {
      setLoading(false);
      setSubmitted(true);
      confetti({
        particleCount: 80,
        spread: 70,
        origin: { y: 0.6 },
        colors: ["#0f172a", "#b45309", "#38bdf8"],
      });
    }, 600);
  };

  const handleReset = () => {
    setSubmitted(false);
    setEmail("");
    setName("");
    onClose();
  };

  return (
    <AnimatePresence>
      {isOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 sm:p-6">
          {/* Backdrop */}
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={onClose}
            className="fixed inset-0 bg-slate-900/60 backdrop-blur-sm"
          />

          {/* Dialog Content */}
          <motion.div
            initial={{ opacity: 0, scale: 0.95, y: 10 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.95, y: 10 }}
            className="relative w-full max-w-lg overflow-hidden rounded-2xl bg-white p-6 sm:p-8 shadow-2xl border border-slate-200 z-10"
          >
            {/* Close Button */}
            <button
              onClick={onClose}
              className="absolute top-4 right-4 p-2 rounded-full text-slate-400 hover:text-slate-700 hover:bg-slate-100 transition-colors"
              aria-label="Close modal"
            >
              <X className="w-5 h-5" />
            </button>

            {!submitted ? (
              <div>
                <div className="flex items-center gap-2 mb-3">
                  <div className="flex items-center justify-center w-8 h-8 rounded-full bg-amber-100 text-amber-800 font-mono text-xs">
                    <Film className="w-4 h-4" />
                  </div>
                  <span className="text-xs font-mono tracking-widest text-slate-500 uppercase">
                    Early Access Program
                  </span>
                </div>

                <h3 className="text-2xl sm:text-3xl font-bold tracking-tight text-slate-900 mb-2">
                  Be Among the First
                </h3>
                <p className="text-sm text-slate-600 mb-6">
                  Join the Big Film Fund community to gain priority access to upcoming film opportunities, creator tools, and ecosystem updates.
                </p>

                <form onSubmit={handleSubmit} className="space-y-4">
                  <div>
                    <label className="block text-xs font-mono text-slate-600 uppercase mb-1">
                      Full Name
                    </label>
                    <input
                      type="text"
                      required
                      placeholder="e.g. Sarah Jenkins"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      className="w-full px-4 py-3 rounded-lg border border-slate-200 bg-slate-50 text-slate-900 text-sm focus:outline-none focus:ring-2 focus:ring-slate-900 focus:bg-white transition-all"
                    />
                  </div>

                  <div>
                    <label className="block text-xs font-mono text-slate-600 uppercase mb-1">
                      Email Address
                    </label>
                    <input
                      type="email"
                      required
                      placeholder="sarah@example.com"
                      value={email}
                      onChange={(e) => setEmail(e.target.value)}
                      className="w-full px-4 py-3 rounded-lg border border-slate-200 bg-slate-50 text-slate-900 text-sm focus:outline-none focus:ring-2 focus:ring-slate-900 focus:bg-white transition-all"
                    />
                  </div>

                  <div>
                    <label className="block text-xs font-mono text-slate-600 uppercase mb-2">
                      Primary Interest
                    </label>
                    <div className="grid grid-cols-3 gap-2">
                      <button
                        type="button"
                        onClick={() => setRole("backer")}
                        className={`px-3 py-2 text-xs rounded-lg font-medium border transition-all ${
                          role === "backer"
                            ? "bg-slate-900 text-white border-slate-900 shadow-sm"
                            : "bg-slate-50 text-slate-700 border-slate-200 hover:border-slate-300"
                        }`}
                      >
                        Film Fan / Backer
                      </button>
                      <button
                        type="button"
                        onClick={() => setRole("creator")}
                        className={`px-3 py-2 text-xs rounded-lg font-medium border transition-all ${
                          role === "creator"
                            ? "bg-slate-900 text-white border-slate-900 shadow-sm"
                            : "bg-slate-50 text-slate-700 border-slate-200 hover:border-slate-300"
                        }`}
                      >
                        Filmmaker / Creator
                      </button>
                      <button
                        type="button"
                        onClick={() => setRole("investor")}
                        className={`px-3 py-2 text-xs rounded-lg font-medium border transition-all ${
                          role === "investor"
                            ? "bg-slate-900 text-white border-slate-900 shadow-sm"
                            : "bg-slate-50 text-slate-700 border-slate-200 hover:border-slate-300"
                        }`}
                      >
                        Industry Investor
                      </button>
                    </div>
                  </div>

                  <button
                    type="submit"
                    disabled={loading}
                    className="w-full mt-4 inline-flex items-center justify-center gap-2 px-6 py-3.5 rounded-lg bg-slate-900 text-white font-medium text-sm hover:bg-slate-800 transition-colors shadow-lg shadow-slate-900/10 disabled:opacity-70"
                  >
                    {loading ? (
                      <span className="inline-block w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                      <>
                        <span>Join the Waitlist</span>
                        <ArrowRight className="w-4 h-4" />
                      </>
                    )}
                  </button>

                  <p className="text-center text-xs text-slate-400 mt-2">
                    No commitment. Just early access.
                  </p>
                </form>
              </div>
            ) : (
              <div className="text-center py-6">
                <div className="mx-auto w-14 h-14 rounded-full bg-emerald-100 flex items-center justify-center text-emerald-600 mb-4">
                  <CheckCircle2 className="w-8 h-8" />
                </div>
                <h3 className="text-2xl font-bold text-slate-900 mb-2">
                  You're on the list!
                </h3>
                <p className="text-sm text-slate-600 max-w-xs mx-auto mb-6">
                  Thank you, <span className="font-semibold text-slate-900">{name || "friend"}</span>. We've reserved your early access spot. Watch your inbox for updates.
                </p>
                <div className="p-4 rounded-xl bg-slate-50 border border-slate-200 text-left text-xs space-y-2 mb-6">
                  <div className="flex justify-between text-slate-500">
                    <span>Assigned Priority:</span>
                    <span className="font-mono text-slate-900 font-semibold">Tier 1 Founder Access</span>
                  </div>
                  <div className="flex justify-between text-slate-500">
                    <span>Role Selected:</span>
                    <span className="font-mono text-slate-900 capitalize">{role}</span>
                  </div>
                </div>
                <button
                  onClick={handleReset}
                  className="px-6 py-2.5 rounded-lg bg-slate-900 text-white text-xs font-medium hover:bg-slate-800 transition-colors"
                >
                  Done
                </button>
              </div>
            )}
          </motion.div>
        </div>
      )}
    </AnimatePresence>
  );
}
