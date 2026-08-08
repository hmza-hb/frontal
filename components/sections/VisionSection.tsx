"use client";

import { useState } from "react";
import { motion } from "framer-motion";
import Image from "next/image";
import { Sparkles, Check, ArrowRight, ShieldCheck, Zap } from "lucide-react";

export default function VisionSection() {
  const [activeTab, setActiveTab] = useState<"bff" | "legacy">("bff");

  return (
    <section id="vision" className="py-24 md:py-32 bg-white border-b border-slate-200/60 relative overflow-hidden">
      <div className="max-w-6xl mx-auto px-4 sm:px-6">
        
        {/* Section Tag */}
        <div className="inline-flex items-center gap-2 px-3 py-1 rounded-md bg-amber-100 text-amber-800 font-mono text-xs uppercase tracking-wider mb-6">
          <Sparkles className="w-3.5 h-3.5" />
          <span>— SECTION 3: THE VISION</span>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-12 gap-12 lg:gap-16 items-center mb-16">
          
          {/* Left Column: Headlines & Body */}
          <div className="lg:col-span-6 space-y-6">
            <h2 className="text-3xl sm:text-4xl md:text-5xl font-extrabold tracking-tight text-slate-900 leading-tight">
              We Believe Entertainment Should Be Open to Everyone.
            </h2>

            <p className="text-lg text-slate-600 font-normal leading-relaxed">
              Big Film Fund is creating the marketplace where entertainment capital meets global communities.
            </p>

            <p className="text-base text-slate-600 font-normal leading-relaxed">
              A future where creators gain new opportunities, audiences become active participants, and groundbreaking stories can find support before they become global successes.
            </p>

            {/* Short Statement Callout Box */}
            <div className="p-6 rounded-2xl bg-slate-900 text-white space-y-2 border border-slate-800 shadow-xl">
              <div className="text-xs font-mono text-amber-400 uppercase tracking-widest">
                Core Philosophy
              </div>
              <p className="text-xl sm:text-2xl font-bold tracking-tight text-white leading-snug">
                "Not just investing in films. Building the future of entertainment ownership."
              </p>
            </div>
          </div>

          {/* Right Column: Editorial Image Frame */}
          <div className="lg:col-span-6 relative">
            <div className="relative rounded-2xl overflow-hidden border border-slate-200 bg-slate-50 p-3 shadow-xl">
              <div className="relative aspect-[4/3] rounded-xl overflow-hidden">
                <Image
                  src="/images/vision-community.png"
                  alt="Big Film Fund — Open Entertainment Community & Visionaries"
                  fill
                  className="object-cover"
                />
              </div>
              <div className="p-4 flex items-center justify-between text-xs font-mono text-slate-600 border-t border-slate-200 mt-2">
                <span>[COMMUNITY LOFT STUDIO]</span>
                <span className="text-amber-700 font-semibold">SHARED PARTICIPATION MODEL</span>
              </div>
            </div>
          </div>

        </div>

        {/* Interactive Comparison Widget */}
        <div className="mt-12 p-6 sm:p-8 rounded-2xl bg-slate-50 border border-slate-200">
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8 pb-6 border-b border-slate-200">
            <div>
              <h3 className="text-xl font-bold text-slate-900">
                Model Shift: Capital Allocation Paradigm
              </h3>
              <p className="text-xs font-mono text-slate-500 mt-1">
                Comparing Legacy Studio Gatekeeping vs Big Film Fund Ecosystem
              </p>
            </div>

            {/* Selector Buttons */}
            <div className="flex items-center gap-1 bg-white p-1 rounded-xl border border-slate-200 shadow-sm">
              <button
                onClick={() => setActiveTab("bff")}
                className={`px-4 py-2 rounded-lg text-xs font-bold transition-all ${
                  activeTab === "bff"
                    ? "bg-slate-900 text-white shadow-md"
                    : "text-slate-600 hover:text-slate-900"
                }`}
              >
                BFF Open Model
              </button>
              <button
                onClick={() => setActiveTab("legacy")}
                className={`px-4 py-2 rounded-lg text-xs font-bold transition-all ${
                  activeTab === "legacy"
                    ? "bg-slate-900 text-white shadow-md"
                    : "text-slate-600 hover:text-slate-900"
                }`}
              >
                Legacy Studio System
              </button>
            </div>
          </div>

          {/* Model Breakdown Grid */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            {activeTab === "bff" ? (
              <>
                <div className="p-5 rounded-xl bg-white border border-slate-200 shadow-sm space-y-2">
                  <div className="text-xs font-mono text-amber-700 font-bold uppercase">01. Access</div>
                  <h4 className="font-bold text-slate-900">Global & Inclusive</h4>
                  <p className="text-xs text-slate-600">Anyone can discover and support films early in development.</p>
                </div>
                <div className="p-5 rounded-xl bg-white border border-slate-200 shadow-sm space-y-2">
                  <div className="text-xs font-mono text-amber-700 font-bold uppercase">02. Creator Freedom</div>
                  <h4 className="font-bold text-slate-900">Direct Audience Backing</h4>
                  <p className="text-xs text-slate-600">Filmmakers retain creative vision without compromise.</p>
                </div>
                <div className="p-5 rounded-xl bg-white border border-slate-200 shadow-sm space-y-2">
                  <div className="text-xs font-mono text-amber-700 font-bold uppercase">03. Relationship</div>
                  <h4 className="font-bold text-slate-900">Active Participation</h4>
                  <p className="text-xs text-slate-600">Supporters follow production, receive updates & participate in success.</p>
                </div>
              </>
            ) : (
              <>
                <div className="p-5 rounded-xl bg-white/60 border border-slate-200 space-y-2 opacity-75">
                  <div className="text-xs font-mono text-slate-400 font-bold uppercase">01. Access</div>
                  <h4 className="font-bold text-slate-700">Closed Door Insiders</h4>
                  <p className="text-xs text-slate-500">Restricted to studio executives and institutional syndicates.</p>
                </div>
                <div className="p-5 rounded-xl bg-white/60 border border-slate-200 space-y-2 opacity-75">
                  <div className="text-xs font-mono text-slate-400 font-bold uppercase">02. Creator Freedom</div>
                  <h4 className="font-bold text-slate-700">Executive Interference</h4>
                  <p className="text-xs text-slate-500">Stories diluted to fit formulaic commercial templates.</p>
                </div>
                <div className="p-5 rounded-xl bg-white/60 border border-slate-200 space-y-2 opacity-75">
                  <div className="text-xs font-mono text-slate-400 font-bold uppercase">03. Relationship</div>
                  <h4 className="font-bold text-slate-700">Passive Consumption</h4>
                  <p className="text-xs text-slate-500">Audiences buy tickets after everything has already been decided.</p>
                </div>
              </>
            )}
          </div>
        </div>

      </div>
    </section>
  );
}
