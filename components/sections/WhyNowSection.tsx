"use client";

import { motion } from "framer-motion";
import { Clock, TrendingUp, Smartphone, Layers, Quote } from "lucide-react";

export default function WhyNowSection() {
  const drivers = [
    {
      icon: TrendingUp,
      title: "Rise of Creator Economies",
      description:
        "Creators are directly connecting with passionate audiences, bypassing traditional legacy intermediaries.",
    },
    {
      icon: Smartphone,
      title: "Digital Platform Scale",
      description:
        "Modern digital infrastructure allows global communities to discover, fund, and engage in real-time.",
    },
    {
      icon: Layers,
      title: "Democratized Capital",
      description:
        "Alternative financing models have proven that community-driven capital outperforms closed room syndicates.",
    },
  ];

  return (
    <section id="why-now" className="py-24 md:py-32 bg-slate-900 text-white relative overflow-hidden">
      
      {/* Background Architectural Glow */}
      <div className="absolute top-0 right-0 w-[500px] h-[500px] bg-gradient-to-bl from-amber-500/10 via-amber-900/5 to-transparent blur-3xl pointer-events-none" />

      <div className="max-w-6xl mx-auto px-4 sm:px-6 relative z-10">
        
        {/* Section Tag */}
        <div className="inline-flex items-center gap-2 px-3 py-1 rounded-md bg-amber-500/20 text-amber-300 font-mono text-xs uppercase tracking-wider mb-6 border border-amber-500/30">
          <Clock className="w-3.5 h-3.5" />
          <span>— SECTION 6: WHY NOW</span>
        </div>

        <div className="max-w-3xl space-y-6 mb-16">
          <h2 className="text-3xl sm:text-4xl md:text-5xl font-extrabold tracking-tight text-white leading-tight">
            The Entertainment Industry Is Ready for Its Next Evolution.
          </h2>

          <p className="text-lg text-slate-300 font-normal leading-relaxed">
            Technology has transformed how we create, distribute, and consume entertainment. Now, the way entertainment is funded is ready for transformation.
          </p>

          <p className="text-base text-slate-400 font-normal leading-relaxed">
            The rise of creator economies, digital platforms, and community-driven investing has created the perfect moment for a new model.
          </p>
        </div>

        {/* 3 Macro Market Drivers */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-16">
          {drivers.map((driver, idx) => {
            const Icon = driver.icon;
            return (
              <motion.div
                key={driver.title}
                initial={{ opacity: 0, y: 20 }}
                whileInView={{ opacity: 1, y: 0 }}
                viewport={{ once: true }}
                transition={{ duration: 0.5, delay: idx * 0.15 }}
                className="p-6 rounded-2xl bg-slate-800/80 border border-slate-700/80 hover:border-amber-500/50 transition-all duration-300 space-y-4"
              >
                <div className="w-10 h-10 rounded-lg bg-slate-700 flex items-center justify-center text-amber-400">
                  <Icon className="w-5 h-5" />
                </div>
                <h3 className="text-lg font-bold text-white">{driver.title}</h3>
                <p className="text-sm text-slate-400 leading-relaxed">{driver.description}</p>
              </motion.div>
            );
          })}
        </div>

        {/* Prominent Closing Statement Box */}
        <div className="relative p-8 sm:p-12 rounded-3xl bg-gradient-to-r from-slate-800 to-slate-900 border border-slate-700 shadow-2xl text-center space-y-4 max-w-4xl mx-auto">
          <Quote className="w-10 h-10 text-amber-400/40 mx-auto" />
          <h3 className="text-2xl sm:text-3xl md:text-4xl font-extrabold tracking-tight text-white leading-snug">
            "The future of entertainment will not be built by a few insiders. It will be built by everyone who believes in great stories."
          </h3>
          <div className="text-xs font-mono text-amber-400 uppercase tracking-widest pt-2">
            Big Film Fund Positioning Thesis
          </div>
        </div>

      </div>
    </section>
  );
}
